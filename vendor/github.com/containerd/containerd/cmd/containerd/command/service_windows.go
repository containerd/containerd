/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package command

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"unsafe"

	"github.com/containerd/containerd/services/server"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

var (
	serviceNameFlag       string
	registerServiceFlag   bool
	unregisterServiceFlag bool
	runServiceFlag        bool

	setStdHandle = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetStdHandle")
	oldStderr    windows.Handle
	panicFile    *os.File

	service *handler
)

const (
	// These should match the values in event_messages.mc.
	eventInfo  = 1
	eventWarn  = 1
	eventError = 1
	eventDebug = 2
	eventPanic = 3
	eventFatal = 4

	eventExtraOffset = 10 // Add this to any event to get a string that supports extended data
)

// serviceFlags returns an array of flags for configuring containerd to run
// as a Windows service under control of SCM.
func serviceFlags() []cli.Flag {
	return []cli.Flag{
		cli.StringFlag{
			Name:  "service-name",
			Usage: "Set the Windows service name",
			Value: "containerd",
		},
		cli.BoolFlag{
			Name:  "register-service",
			Usage: "Register the service and exit",
		},
		cli.BoolFlag{
			Name:  "unregister-service",
			Usage: "Unregister the service and exit",
		},
		cli.BoolFlag{
			Name:   "run-service",
			Usage:  "",
			Hidden: true,
		},
	}
}

// applyPlatformFlags applies platform-specific flags.
func applyPlatformFlags(context *cli.Context) {

	if s := context.GlobalString("service-name"); s != "" {
		serviceNameFlag = s
	}
	for _, v := range []struct {
		name string
		d    *bool
	}{
		{
			name: "register-service",
			d:    &registerServiceFlag,
		},
		{
			name: "unregister-service",
			d:    &unregisterServiceFlag,
		},
		{
			name: "run-service",
			d:    &runServiceFlag,
		},
	} {
		*v.d = context.GlobalBool(v.name)
	}
}

type handler struct {
	fromsvc chan error
	s       *server.Server
	done    chan struct{} // Indicates back to app main to quit
}

type etwHook struct {
	log *eventlog.Log
}

func (h *etwHook) Levels() []logrus.Level {
	return []logrus.Level{
		logrus.PanicLevel,
		logrus.FatalLevel,
		logrus.ErrorLevel,
		logrus.WarnLevel,
		logrus.InfoLevel,
		logrus.DebugLevel,
	}
}

func (h *etwHook) Fire(e *logrus.Entry) error {
	var (
		etype uint16
		eid   uint32
	)

	switch e.Level {
	case logrus.PanicLevel:
		etype = windows.EVENTLOG_ERROR_TYPE
		eid = eventPanic
	case logrus.FatalLevel:
		etype = windows.EVENTLOG_ERROR_TYPE
		eid = eventFatal
	case logrus.ErrorLevel:
		etype = windows.EVENTLOG_ERROR_TYPE
		eid = eventError
	case logrus.WarnLevel:
		etype = windows.EVENTLOG_WARNING_TYPE
		eid = eventWarn
	case logrus.InfoLevel:
		etype = windows.EVENTLOG_INFORMATION_TYPE
		eid = eventInfo
	case logrus.DebugLevel:
		etype = windows.EVENTLOG_INFORMATION_TYPE
		eid = eventDebug
	default:
		return errors.New("unknown level")
	}

	// If there is additional data, include it as a second string.
	exts := ""
	if len(e.Data) > 0 {
		fs := bytes.Buffer{}
		for k, v := range e.Data {
			fs.WriteString(k)
			fs.WriteByte('=')
			fmt.Fprint(&fs, v)
			fs.WriteByte(' ')
		}

		exts = fs.String()[:fs.Len()-1]
		eid += eventExtraOffset
	}

	if h.log == nil {
		fmt.Fprintf(os.Stderr, "%s [%s]\n", e.Message, exts)
		return nil
	}

	var (
		ss  [2]*uint16
		err error
	)

	ss[0], err = windows.UTF16PtrFromString(e.Message)
	if err != nil {
		return err
	}

	count := uint16(1)
	if exts != "" {
		ss[1], err = windows.UTF16PtrFromString(exts)
		if err != nil {
			return err
		}

		count++
	}

	return windows.ReportEvent(h.log.Handle, etype, 0, eid, 0, count, 0, &ss[0], nil)
}

func getServicePath() (string, error) {
	p, err := exec.LookPath(os.Args[0])
	if err != nil {
		return "", err
	}
	return filepath.Abs(p)
}

func registerService() error {
	p, err := getServicePath()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	c := mgr.Config{
		ServiceType:  windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
		DisplayName:  "Containerd",
		Description:  "Container runtime",
	}

	// Configure the service to launch with the arguments that were just passed.
	args := []string{"--run-service"}
	for _, a := range os.Args[1:] {
		if a != "--register-service" && a != "--unregister-service" {
			args = append(args, a)
		}
	}

	s, err := m.CreateService(serviceNameFlag, p, c, args...)
	if err != nil {
		return err
	}
	defer s.Close()

	// See http://stackoverflow.com/questions/35151052/how-do-i-configure-failure-actions-of-a-windows-service-written-in-go
	const (
		scActionNone    = 0
		scActionRestart = 1

		serviceConfigFailureActions = 2
	)

	type serviceFailureActions struct {
		ResetPeriod  uint32
		RebootMsg    *uint16
		Command      *uint16
		ActionsCount uint32
		Actions      uintptr
	}

	type scAction struct {
		Type  uint32
		Delay uint32
	}
	t := []scAction{
		{Type: scActionRestart, Delay: uint32(60 * time.Second / time.Millisecond)},
		{Type: scActionRestart, Delay: uint32(60 * time.Second / time.Millisecond)},
		{Type: scActionNone},
	}
	lpInfo := serviceFailureActions{ResetPeriod: uint32(24 * time.Hour / time.Second), ActionsCount: uint32(3), Actions: uintptr(unsafe.Pointer(&t[0]))}
	err = windows.ChangeServiceConfig2(s.Handle, serviceConfigFailureActions, (*byte)(unsafe.Pointer(&lpInfo)))
	if err != nil {
		return err
	}

	return eventlog.Install(serviceNameFlag, p, false, eventlog.Info|eventlog.Warning|eventlog.Error)
}

func unregisterService() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceNameFlag)
	if err != nil {
		return err
	}
	defer s.Close()

	eventlog.Remove(serviceNameFlag)
	err = s.Delete()
	if err != nil {
		return err
	}
	return nil
}

// registerUnregisterService is an entrypoint early in the daemon startup
// to handle (un-)registering against Windows Service Control Manager (SCM).
// It returns an indication to stop on successful SCM operation, and an error.
func registerUnregisterService(root string) (bool, error) {

	if unregisterServiceFlag {
		if registerServiceFlag {
			return true, errors.New("--register-service and --unregister-service cannot be used together")
		}
		return true, unregisterService()
	}

	if registerServiceFlag {
		return true, registerService()
	}

	if runServiceFlag {
		if err := initPanicFile(filepath.Join(root, "panic.log")); err != nil {
			return true, err
		}

		interactive, err := svc.IsAnInteractiveSession()
		if err != nil {
			return true, err
		}

		var log *eventlog.Log
		if !interactive {
			log, err = eventlog.Open(serviceNameFlag)
			if err != nil {
				return true, err
			}
		}

		logrus.AddHook(&etwHook{log})
		logrus.SetOutput(ioutil.Discard)

	}
	return false, nil
}

// launchService is the entry point for running the daemon under SCM.
func launchService(s *server.Server, done chan struct{}) error {

	if !runServiceFlag {
		return nil
	}

	h := &handler{
		fromsvc: make(chan error),
		s:       s,
		done:    done,
	}

	interactive, err := svc.IsAnInteractiveSession()
	if err != nil {
		return err
	}

	service = h
	go func() {
		if interactive {
			err = debug.Run(serviceNameFlag, h)
		} else {
			err = svc.Run(serviceNameFlag, h)
		}
		h.fromsvc <- err
	}()

	// Wait for the first signal from the service handler.
	err = <-h.fromsvc
	if err != nil {
		return err
	}
	return nil
}

func (h *handler) Execute(_ []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	s <- svc.Status{State: svc.StartPending, Accepts: 0}
	// Unblock launchService()
	h.fromsvc <- nil

	s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.Accepted(windows.SERVICE_ACCEPT_PARAMCHANGE)}

Loop:
	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			s <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			s <- svc.Status{State: svc.StopPending, Accepts: 0}
			h.s.Stop()
			break Loop
		}
	}

	removePanicFile()
	close(h.done)
	return false, 0
}

func initPanicFile(path string) error {
	var err error
	panicFile, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}

	st, err := panicFile.Stat()
	if err != nil {
		return err
	}

	// If there are contents in the file already, move the file out of the way
	// and replace it.
	if st.Size() > 0 {
		panicFile.Close()
		os.Rename(path, path+".old")
		panicFile, err = os.Create(path)
		if err != nil {
			return err
		}
	}

	// Update STD_ERROR_HANDLE to point to the panic file so that Go writes to
	// it when it panics. Remember the old stderr to restore it before removing
	// the panic file.
	sh := windows.STD_ERROR_HANDLE
	h, err := windows.GetStdHandle(uint32(sh))
	if err != nil {
		return err
	}

	oldStderr = h

	r, _, err := setStdHandle.Call(uintptr(sh), panicFile.Fd())
	if r == 0 && err != nil {
		return err
	}

	// Reset os.Stderr to the panic file (so fmt.Fprintf(os.Stderr,...) actually gets redirected)
	os.Stderr = os.NewFile(panicFile.Fd(), "/dev/stderr")

	// Force threads that panic to write to stderr (the panicFile handle now), otherwise it will go into the ether
	log.SetOutput(os.Stderr)

	return nil
}

func removePanicFile() {
	if st, err := panicFile.Stat(); err == nil {
		if st.Size() == 0 {
			sh := windows.STD_ERROR_HANDLE
			setStdHandle.Call(uintptr(sh), uintptr(oldStderr))
			panicFile.Close()
			os.Remove(panicFile.Name())
		}
	}
}
