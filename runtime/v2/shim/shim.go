// +build !windows

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

package shim

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/namespaces"
	shimapi "github.com/containerd/containerd/runtime/v2/task"
	"github.com/containerd/ttrpc"
	"github.com/containerd/typeurl"
	"github.com/gogo/protobuf/proto"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

// Client for a shim server
type Client struct {
	service shimapi.TaskService
	context context.Context
	signals chan os.Signal
}

// Init func for the creation of a shim server
type Init func(context.Context, string, events.Publisher) (Shim, error)

// Shim server interface
type Shim interface {
	shimapi.TaskService
	Cleanup(ctx context.Context) (*shimapi.DeleteResponse, error)
	StartShim(ctx context.Context, id, containerdBinary, containerdAddress string) (string, error)
}

var (
	debugFlag            bool
	idFlag               string
	namespaceFlag        string
	socketFlag           string
	addressFlag          string
	containerdBinaryFlag string
	action               string
)

func parseFlags() {
	flag.BoolVar(&debugFlag, "debug", false, "enable debug output in logs")
	flag.StringVar(&namespaceFlag, "namespace", "", "namespace that owns the shim")
	flag.StringVar(&idFlag, "id", "", "id of the task")
	flag.StringVar(&socketFlag, "socket", "", "abstract socket path to serve")

	flag.StringVar(&addressFlag, "address", "", "grpc address back to main containerd")
	flag.StringVar(&containerdBinaryFlag, "publish-binary", "containerd", "path to publish binary (used for publishing events)")

	flag.Parse()
	action = flag.Arg(0)
}

func setRuntime() {
	debug.SetGCPercent(40)
	go func() {
		for range time.Tick(30 * time.Second) {
			debug.FreeOSMemory()
		}
	}()
	if debugFlag {
		f, err := os.OpenFile("shim.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open shim log %s", err)
			os.Exit(1)
		}
		logrus.SetLevel(logrus.DebugLevel)
		logrus.SetOutput(f)
	}
	if os.Getenv("GOMAXPROCS") == "" {
		// If GOMAXPROCS hasn't been set, we default to a value of 2 to reduce
		// the number of Go stacks present in the shim.
		runtime.GOMAXPROCS(2)
	}
}

// Run initializes and runs a shim server
func Run(initFunc Init) error {
	parseFlags()
	setRuntime()

	signals, err := setupSignals()
	if err != nil {
		return err
	}
	if err := subreaper(); err != nil {
		return err
	}
	publisher := &remoteEventsPublisher{
		address:              addressFlag,
		containerdBinaryPath: containerdBinaryFlag,
	}
	if namespaceFlag == "" {
		return fmt.Errorf("shim namespace cannot be empty")
	}
	ctx := namespaces.WithNamespace(context.Background(), namespaceFlag)
	service, err := initFunc(ctx, idFlag, publisher)
	if err != nil {
		return err
	}
	switch action {
	case "delete":
		go handleSignals(signals)
		response, err := service.Cleanup(ctx)
		if err != nil {
			return err
		}
		data, err := proto.Marshal(response)
		if err != nil {
			return err
		}
		if _, err := os.Stdout.Write(data); err != nil {
			return err
		}
		return nil
	case "start":
		address, err := service.StartShim(ctx, idFlag, containerdBinaryFlag, addressFlag)
		if err != nil {
			return err
		}
		if _, err := os.Stdout.WriteString(address); err != nil {
			return err
		}
		return nil
	default:
		client := NewShimClient(ctx, service, signals)
		return client.Serve()
	}
}

// NewShimClient creates a new shim server client
func NewShimClient(ctx context.Context, svc shimapi.TaskService, signals chan os.Signal) *Client {
	s := &Client{
		service: svc,
		context: ctx,
		signals: signals,
	}
	return s
}

// Serve the shim server
func (s *Client) Serve() error {
	dump := make(chan os.Signal, 32)
	signal.Notify(dump, syscall.SIGUSR1)

	path, err := os.Getwd()
	if err != nil {
		return err
	}
	server, err := newServer()
	if err != nil {
		return errors.Wrap(err, "failed creating server")
	}

	logrus.Debug("registering ttrpc server")
	shimapi.RegisterTaskService(server, s.service)

	if err := serve(s.context, server, socketFlag); err != nil {
		return err
	}
	logger := logrus.WithFields(logrus.Fields{
		"pid":       os.Getpid(),
		"path":      path,
		"namespace": namespaceFlag,
	})
	go func() {
		for range dump {
			dumpStacks(logger)
		}
	}()
	return handleSignals(s.signals)
}

// serve serves the ttrpc API over a unix socket at the provided path
// this function does not block
func serve(ctx context.Context, server *ttrpc.Server, path string) error {
	var (
		l   net.Listener
		err error
	)
	if path == "" {
		l, err = net.FileListener(os.NewFile(3, "socket"))
		path = "[inherited from parent]"
	} else {
		if len(path) > 106 {
			return errors.Errorf("%q: unix socket path too long (> 106)", path)
		}
		l, err = net.Listen("unix", "\x00"+path)
	}
	if err != nil {
		return err
	}
	logrus.WithField("socket", path).Debug("serving api on unix socket")
	go func() {
		defer l.Close()
		if err := server.Serve(ctx, l); err != nil &&
			!strings.Contains(err.Error(), "use of closed network connection") {
			logrus.WithError(err).Fatal("containerd-shim: ttrpc server failure")
		}
	}()
	return nil
}

func handleSignals(signals chan os.Signal) error {
	for {
		select {
		case s := <-signals:
			switch s {
			case unix.SIGCHLD:
				if err := Default.Reap(); err != nil {
					logrus.WithError(err).Error("reap exit status")
				}
			case unix.SIGPIPE:
			}
		}
	}
}

func dumpStacks(logger *logrus.Entry) {
	var (
		buf       []byte
		stackSize int
	)
	bufferLen := 16384
	for stackSize == len(buf) {
		buf = make([]byte, bufferLen)
		stackSize = runtime.Stack(buf, true)
		bufferLen *= 2
	}
	buf = buf[:stackSize]
	logger.Infof("=== BEGIN goroutine stack dump ===\n%s\n=== END goroutine stack dump ===", buf)
}

type remoteEventsPublisher struct {
	address              string
	containerdBinaryPath string
}

func (l *remoteEventsPublisher) Publish(ctx context.Context, topic string, event events.Event) error {
	ns, _ := namespaces.Namespace(ctx)
	encoded, err := typeurl.MarshalAny(event)
	if err != nil {
		return err
	}
	data, err := encoded.Marshal()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, l.containerdBinaryPath, "--address", l.address, "publish", "--topic", topic, "--namespace", ns)
	cmd.Stdin = bytes.NewReader(data)
	c, err := Default.Start(cmd)
	if err != nil {
		return err
	}
	status, err := Default.Wait(cmd, c)
	if err != nil {
		return err
	}
	if status != 0 {
		return errors.New("failed to publish event")
	}
	return nil
}
