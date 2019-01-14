// +build windows

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

package runhcs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	winio "github.com/Microsoft/go-winio"
	"github.com/Microsoft/hcsshim/pkg/go-runhcs"
	eventstypes "github.com/containerd/containerd/api/events"
	containerd_types "github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/api/types/task"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/runtime/v2/runhcs/options"
	"github.com/containerd/containerd/runtime/v2/shim"
	taskAPI "github.com/containerd/containerd/runtime/v2/task"
	"github.com/containerd/go-runc"
	"github.com/containerd/typeurl"
	ptypes "github.com/gogo/protobuf/types"
	oci "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	runhcsShimVersion = "0.0.1"
	safePipePrefix    = `\\.\pipe\ProtectedPrefix\Administrators\`

	errorConnectionAborted syscall.Errno = 1236
)

var (
	empty = &ptypes.Empty{}
)

// forwardRunhcsLogs copies logs from c and writes them to the ctx logger
// upstream.
func forwardRunhcsLogs(ctx context.Context, c net.Conn, fields logrus.Fields) {
	defer c.Close()
	j := json.NewDecoder(c)

	for {
		e := logrus.Entry{}
		err := j.Decode(&e.Data)
		if err == io.EOF || err == errorConnectionAborted {
			break
		}
		if err != nil {
			// Likely the last message wasn't complete at closure. Just read all
			// data and forward as error.
			data, _ := ioutil.ReadAll(io.MultiReader(j.Buffered(), c))
			if len(data) != 0 {
				log.G(ctx).WithFields(fields).Error(string(data))
			}
			break
		}

		msg := e.Data[logrus.FieldKeyMsg]
		delete(e.Data, logrus.FieldKeyMsg)

		level, err := logrus.ParseLevel(e.Data[logrus.FieldKeyLevel].(string))
		if err != nil {
			log.G(ctx).WithFields(fields).WithError(err).Debug("invalid log level")
			level = logrus.DebugLevel
		}
		delete(e.Data, logrus.FieldKeyLevel)

		// TODO: JTERRY75 maybe we need to make this configurable so we know
		// that runhcs is using the same one we are deserializing.
		ti, err := time.Parse(time.RFC3339, e.Data[logrus.FieldKeyTime].(string))
		if err != nil {
			log.G(ctx).WithFields(fields).WithError(err).Debug("invalid time stamp format")
			ti = time.Time{}
		}
		delete(e.Data, logrus.FieldKeyTime)

		etr := log.G(ctx).WithFields(fields).WithFields(e.Data)
		etr.Time = ti
		switch level {
		case logrus.PanicLevel:
			etr.Panic(msg)
		case logrus.FatalLevel:
			etr.Fatal(msg)
		case logrus.ErrorLevel:
			etr.Error(msg)
		case logrus.WarnLevel:
			etr.Warn(msg)
		case logrus.InfoLevel:
			etr.Info(msg)
		case logrus.DebugLevel:
			etr.Debug(msg)
		}
	}
}

// New returns a new runhcs shim service that can be used via GRPC
func New(ctx context.Context, id string, publisher events.Publisher) (shim.Shim, error) {
	return &service{
		context:   ctx,
		id:        id,
		processes: make(map[string]*process),
		publisher: publisher,
	}, nil
}

var _ = (taskAPI.TaskService)(&service{})
var _ = (shim.Shim)(&service{})

// service is the runhcs shim implementation of the v2 TaskService over GRPC
type service struct {
	mu sync.Mutex

	context context.Context

	// debugLog if not "" indicates the log file or pipe path for runhcs.exe to
	// write its logs to.
	debugLog string
	// if `shimOpts.DebugType == options.Opitons_NPIPE` will hold the listener
	// for the runhcs.exe to connect to for sending logs.
	debugListener net.Listener

	shimOpts options.Options

	id        string
	processes map[string]*process

	publisher events.Publisher
}

func (s *service) newRunhcs() *runhcs.Runhcs {
	return &runhcs.Runhcs{
		Debug:     s.shimOpts.Debug,
		Log:       s.debugLog,
		LogFormat: runhcs.JSON,
		Owner:     "containerd-runhcs-shim-v1",
		Root:      s.shimOpts.RegistryRoot,
	}
}

// getProcess attempts to get a process by id.
// The caller MUST NOT have locked s.mu previous to calling this function.
func (s *service) getProcess(id, execID string) (*process, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.getProcessLocked(id, execID)
}

// getProcessLocked attempts to get a process by id.
// The caller MUST protect s.mu previous to calling this function.
func (s *service) getProcessLocked(id, execID string) (*process, error) {
	if execID != "" {
		id = execID
	}

	var p *process
	var ok bool
	if p, ok = s.processes[id]; !ok {
		return nil, errdefs.ToGRPCf(errdefs.ErrNotFound, "process %s", id)
	}
	return p, nil
}

func (s *service) Cleanup(ctx context.Context) (*taskAPI.DeleteResponse, error) {
	log.G(ctx).Info("Starting Cleanup")

	_, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	// Forcibly shut down any container in this bundle
	rhcs := s.newRunhcs()
	dopts := &runhcs.DeleteOpts{
		Force: true,
	}
	if err := rhcs.Delete(ctx, s.id, dopts); err != nil {
		log.G(ctx).WithError(err).Debugf("failed to delete container")
	}

	opts, ok := ctx.Value(shim.OptsKey{}).(shim.Opts)
	if ok && opts.BundlePath != "" {
		if err := os.RemoveAll(opts.BundlePath); err != nil {
			return nil, err
		}
	}

	return &taskAPI.DeleteResponse{
		ExitedAt:   time.Now(),
		ExitStatus: 255,
	}, nil
}

func (s *service) StartShim(ctx context.Context, id, containerdBinary, containerdAddress string) (string, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return "", err
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	socketAddress, err := shim.SocketAddress(ctx, id)
	if err != nil {
		return "", err
	}
	args := []string{
		"-namespace", ns,
		"-id", id,
		"-address", containerdAddress,
		"-publish-binary", containerdBinary,
		"-socket", socketAddress,
	}

	opts, ok := ctx.Value(shim.OptsKey{}).(shim.Opts)
	if ok && opts.Debug {
		args = append(args, "-debug")
	}

	cmd := exec.Command(self, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GOMAXPROCS=2")

	if err = cmd.Start(); err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			cmd.Process.Kill()
		}
	}()
	// TODO: JTERRY75 - Windows does not use the ExtraFiles to pass the socket
	// because winio does not support it which exposes a race condition between
	// these two processes. For now AnonDialer will retry if the file does not
	// exist up to the timeout waiting for the shim service to create the pipe.
	go cmd.Wait()
	if err := shim.WritePidFile("shim.pid", cmd.Process.Pid); err != nil {
		return "", err
	}
	if err := shim.WriteAddress("address", socketAddress); err != nil {
		return "", err
	}
	return socketAddress, nil
}

// State returns runtime state information for a process
func (s *service) State(ctx context.Context, r *taskAPI.StateRequest) (*taskAPI.StateResponse, error) {
	log.G(ctx).Debugf("State: %s: %s", r.ID, r.ExecID)

	s.mu.Lock()
	defer s.mu.Unlock()

	var p *process
	var err error
	if p, err = s.getProcessLocked(r.ID, r.ExecID); err != nil {
		return nil, err
	}

	var tstatus string

	// This is a container
	if p.cid == p.id {
		rhcs := s.newRunhcs()
		cs, err := rhcs.State(ctx, p.id)
		if err != nil {
			return nil, err
		}
		tstatus = cs.Status
	} else {
		if p.started {
			tstatus = "running"
		} else {
			tstatus = "created"
		}
	}

	status := task.StatusUnknown
	switch tstatus {
	case "created":
		status = task.StatusCreated
	case "running":
		status = task.StatusRunning
	case "stopped":
		status = task.StatusStopped
	case "paused":
		status = task.StatusPaused
	}
	pe := p.stat()
	return &taskAPI.StateResponse{
		ID:         p.id,
		Bundle:     p.bundle,
		Pid:        pe.pid,
		Status:     status,
		Stdin:      p.stdin,
		Stdout:     p.stdout,
		Stderr:     p.stderr,
		Terminal:   p.terminal,
		ExitStatus: pe.exitStatus,
		ExitedAt:   pe.exitedAt,
	}, nil
}

func writeMountsToConfig(bundle string, mounts []*containerd_types.Mount) error {
	cf, err := os.OpenFile(path.Join(bundle, "config.json"), os.O_RDWR, 0)
	if err != nil {
		return errors.Wrap(err, "bundle does not contain config.json")
	}
	defer cf.Close()
	var spec oci.Spec
	if err := json.NewDecoder(cf).Decode(&spec); err != nil {
		return errors.Wrap(err, "bundle config.json is not valid oci spec")
	}

	if len(mounts) == 0 {
		// If no mounts are passed via the snapshotter its the callers full
		// responsibility to manage the storage. Just move on without affecting
		// the config.json at all.
		if spec.Windows == nil || len(spec.Windows.LayerFolders) < 2 {
			return errors.New("no Windows.LayerFolders found in oci spec")
		}
		return nil
	} else if len(mounts) != 1 {
		return errors.New("Rootfs does not contain exactly 1 mount for the root file system")
	}

	m := mounts[0]
	if m.Type != "windows-layer" && m.Type != "lcow-layer" {
		return errors.Errorf("unsupported mount type '%s'", m.Type)
	}

	// parentLayerPaths are passed in layerN, layerN-1, ..., layer 0
	//
	// The OCI spec expects:
	//   windows-layer order is layerN, layerN-1, ..., layer0, scratch
	//   lcow-layer    order is layer0,   layer1, ..., layerN, scratch
	var parentLayerPaths []string
	for _, option := range mounts[0].Options {
		if strings.HasPrefix(option, mount.ParentLayerPathsFlag) {
			err := json.Unmarshal([]byte(option[len(mount.ParentLayerPathsFlag):]), &parentLayerPaths)
			if err != nil {
				return errors.Wrap(err, "failed to unmarshal parent layer paths from mount")
			}
		}
	}

	if m.Type == "lcow-layer" {
		// Reverse the lcow-layer parents
		for i := len(parentLayerPaths)/2 - 1; i >= 0; i-- {
			opp := len(parentLayerPaths) - 1 - i
			parentLayerPaths[i], parentLayerPaths[opp] = parentLayerPaths[opp], parentLayerPaths[i]
		}

		// If we are creating LCOW make sure that spec.Windows is filled out before
		// appending layer folders.
		if spec.Windows == nil {
			spec.Windows = &oci.Windows{}
		}
		if spec.Windows.HyperV == nil {
			spec.Windows.HyperV = &oci.WindowsHyperV{}
		}
	} else if spec.Windows.HyperV == nil {
		// This is a Windows Argon make sure that we have a Root filled in.
		if spec.Root == nil {
			spec.Root = &oci.Root{}
		}
	}

	// Append the parents
	spec.Windows.LayerFolders = append(spec.Windows.LayerFolders, parentLayerPaths...)
	// Append the scratch
	spec.Windows.LayerFolders = append(spec.Windows.LayerFolders, m.Source)

	if err := cf.Truncate(0); err != nil {
		return errors.Wrap(err, "failed to truncate config.json")
	}
	if _, err := cf.Seek(0, 0); err != nil {
		return errors.Wrap(err, "failed to seek to 0 in config.json")
	}

	if err := json.NewEncoder(cf).Encode(spec); err != nil {
		return errors.Wrap(err, "failed to write Mounts into config.json")
	}
	return nil
}

// Create a new initial process and container with runhcs
func (s *service) Create(ctx context.Context, r *taskAPI.CreateTaskRequest) (*taskAPI.CreateTaskResponse, error) {
	log.G(ctx).Debugf("Create: %s", r.ID)

	// Hold the lock for the entire duration to avoid duplicate process creation.
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "create namespace")
	}

	if r.Options != nil {
		v, err := typeurl.UnmarshalAny(r.Options)
		if err != nil {
			return nil, err
		}
		s.shimOpts = *v.(*options.Options)
	}

	if p := s.processes[r.ID]; p != nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrAlreadyExists, "process %s already exists", r.ID)
	}
	if r.ID != s.id {
		return nil, errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "init process id '%s' != shim id '%s'", r.ID, s.id)
	}

	// Add the mounts to the layer paths
	if err := writeMountsToConfig(r.Bundle, r.Rootfs); err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	// Create the IO for the process
	io, err := runc.NewPipeIO()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create io pipes")
	}
	defer func() {
		if err != nil {
			io.Close()
		}
	}()
	// Create the upstream IO
	ps, err := newPipeSet(ctx, r.Stdin, r.Stdout, r.Stderr, r.Terminal)
	if err != nil {
		return nil, errors.Wrap(err, "failed to connect to upstream IO")
	}
	defer func() {
		if err != nil {
			ps.Close()
		}
	}()
	// Create the relay for them both.
	pr := newPipeRelay(ctx, ps, io)
	defer func() {
		if err != nil {
			pr.close()
		}
	}()

	if s.shimOpts.Debug {
		if s.debugLog == "" {
			if s.shimOpts.DebugType == options.Options_FILE {
				s.debugLog = filepath.Join(r.Bundle, fmt.Sprintf("runhcs-%s.log", r.ID))
			} else {
				logPath := safePipePrefix + fmt.Sprintf("runhcs-log-%s", r.ID)
				l, err := winio.ListenPipe(logPath, nil)
				if err != nil {
					return nil, err
				}
				s.debugLog = logPath
				s.debugListener = l

				// Accept connections and forward all logs for each runhcs.exe
				// invocation
				go func() {
					for {
						c, err := s.debugListener.Accept()
						if err != nil {
							if err == winio.ErrPipeListenerClosed {
								break
							}
							log.G(ctx).WithError(err).Debug("log accept failure")
							// Logrus error locally?
							continue
						}
						fields := map[string]interface{}{
							"log-source": "runhcs",
							"task-id":    r.ID,
						}
						go forwardRunhcsLogs(ctx, c, fields)
					}
				}()
			}
		}
	}

	pidfilePath := path.Join(r.Bundle, "runhcs-pidfile.pid")
	copts := &runhcs.CreateOpts{
		IO:      io,
		PidFile: pidfilePath,
	}
	rhcs := s.newRunhcs()
	if rhcs.Debug {
		if s.shimOpts.DebugType == options.Options_FILE {
			copts.ShimLog = filepath.Join(r.Bundle, fmt.Sprintf("runhcs-shim-%s.log", r.ID))
			copts.VMLog = filepath.Join(r.Bundle, fmt.Sprintf("runhcs-vmshim-%s.log", r.ID))
		} else {
			doForwardLogs := func(source, logPipeFmt string, opt *string) error {
				pipeName := fmt.Sprintf(logPipeFmt, r.ID)
				*opt = safePipePrefix + pipeName
				l, err := winio.ListenPipe(*opt, nil)
				if err != nil {
					return err
				}
				go func() {
					defer l.Close()
					c, err := l.Accept()
					if err != nil {
						log.G(ctx).
							WithField("task-id", r.ID).
							WithError(err).
							Errorf("failed to accept %s", pipeName)
					} else {
						fields := map[string]interface{}{
							"log-source": source,
							"task-id":    r.ID,
						}
						go forwardRunhcsLogs(ctx, c, fields)
					}
				}()
				return nil
			}

			err = doForwardLogs("runhcs-shim", "runhcs-shim-log-%s", &copts.ShimLog)
			if err != nil {
				return nil, err
			}

			err = doForwardLogs("runhcs--vm-shim", "runhcs-vm-shim-log-%s", &copts.VMLog)
			if err != nil {
				return nil, err
			}
		}
	}
	err = rhcs.Create(ctx, r.ID, r.Bundle, copts)
	if err != nil {
		return nil, err
	}

	// We successfully created the process. Convert from initpid to the container process.
	pid, err := runc.ReadPidFile(pidfilePath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read container pid from pidfile")
	}

	process, err := newProcess(
		ctx,
		s,
		r.ID,
		uint32(pid),
		pr,
		r.Bundle,
		r.Stdin,
		r.Stdout,
		r.Stderr,
		r.Terminal)

	if err != nil {
		return nil, err
	}
	s.processes[r.ID] = process

	s.publisher.Publish(ctx,
		runtime.TaskCreateEventTopic,
		&eventstypes.TaskCreate{
			ContainerID: process.id,
			Bundle:      process.bundle,
			Rootfs:      r.Rootfs,
			IO: &eventstypes.TaskIO{
				Stdin:    r.Stdin,
				Stdout:   r.Stdout,
				Stderr:   r.Stderr,
				Terminal: r.Terminal,
			},
			Checkpoint: "",
			Pid:        uint32(pid),
		})

	return &taskAPI.CreateTaskResponse{
		Pid: uint32(pid),
	}, nil
}

// Start a process
func (s *service) Start(ctx context.Context, r *taskAPI.StartRequest) (*taskAPI.StartResponse, error) {
	log.G(ctx).Debugf("Start: %s: %s", r.ID, r.ExecID)
	var p *process
	var err error
	if p, err = s.getProcess(r.ID, r.ExecID); err != nil {
		return nil, err
	}
	p.Lock()
	defer p.Unlock()

	if p.started {
		return nil, errors.New("cannot start already started container or process")
	}

	rhcs := s.newRunhcs()

	// This is a start/exec
	if r.ExecID != "" {
		execFmt := fmt.Sprintf("exec-%s-%s", r.ID, r.ExecID)
		pidfilePath := path.Join(p.bundle, fmt.Sprintf("runhcs-%s-pidfile.pid", execFmt))
		procConfig := path.Join(p.bundle, execFmt+"config.json")
		eopts := &runhcs.ExecOpts{
			IO:      p.relay.io,
			PidFile: pidfilePath,
			Detach:  true,
		}

		if rhcs.Debug {
			if s.shimOpts.DebugType == options.Options_FILE {
				eopts.ShimLog = filepath.Join(p.bundle, fmt.Sprintf("runhcs-shim-%s.log", execFmt))
			} else {
				doForwardLogs := func(source, pipeName string, opt *string) error {
					*opt = safePipePrefix + pipeName
					l, err := winio.ListenPipe(*opt, nil)
					if err != nil {
						return err
					}
					go func() {
						defer l.Close()
						c, err := l.Accept()
						if err != nil {
							log.G(ctx).
								WithField("task-id", r.ID).
								WithField("exec-id", r.ExecID).
								WithError(err).
								Errorf("failed to accept %s", pipeName)
						} else {
							fields := map[string]interface{}{
								"log-source": source,
								"task-id":    r.ID,
								"exec-id":    r.ExecID,
							}
							go forwardRunhcsLogs(ctx, c, fields)
						}
					}()
					return nil
				}

				err = doForwardLogs("runhcs-shim-exec", "runhcs-shim-log-"+execFmt, &eopts.ShimLog)
				if err != nil {
					return nil, err
				}
			}
		}

		// ID here is the containerID to exec the process in.
		err = rhcs.Exec(ctx, r.ID, procConfig, eopts)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to exec process: %s", r.ExecID)
		}

		p.started = true

		// We successfully exec'd the process. Convert from initpid to the container process.
		pid, err := runc.ReadPidFile(pidfilePath)
		if err != nil {
			return nil, errors.Wrap(err, "failed to read container pid from pidfile")
		}

		proc, err := os.FindProcess(pid)
		if err != nil {
			return nil, errors.Wrap(err, "failed to find exec process pid")
		}
		go waitForProcess(ctx, p, proc, s)
	} else {
		if err := rhcs.Start(ctx, p.id); err != nil {
			return nil, errors.Wrapf(err, "failed to start container: %s", p.id)
		}

		p.started = true

		// TODO: JTERRY75 - This is a total hack. We cant return from this call
		// until the state has transitioned. Because runhcs start is long lived it
		// will return before the actual state is "RUNNING" which causes a failure
		// if the 'state' query comes in and it is not in the "RUNNING" state.
		stateRequest := &taskAPI.StateRequest{
			ID:     r.ID,
			ExecID: r.ExecID,
		}
		for {
			sr, err := s.State(ctx, stateRequest)
			if err != nil {
				log.G(ctx).WithError(err).Debug("failed to query state of container")
				break
			}
			// Have we transitioned states yet? Expect != created && != unknown
			if sr.Status != task.StatusCreated && sr.Status != task.StatusUnknown {
				break
			}
			time.Sleep(1 * time.Second)
		}
	}

	pid := p.stat().pid
	if r.ExecID != "" {
		s.publisher.Publish(ctx,
			runtime.TaskExecStartedEventTopic,
			&eventstypes.TaskExecStarted{
				ContainerID: p.cid,
				ExecID:      p.id,
				Pid:         pid,
			})
	} else {
		s.publisher.Publish(ctx,
			runtime.TaskStartEventTopic,
			&eventstypes.TaskStart{
				ContainerID: p.id,
				Pid:         pid,
			})
	}

	p.startedWg.Done()

	return &taskAPI.StartResponse{
		Pid: pid,
	}, nil
}

// Delete the initial process and container
func (s *service) Delete(ctx context.Context, r *taskAPI.DeleteRequest) (*taskAPI.DeleteResponse, error) {
	log.G(ctx).Debugf("Delete: %s: %s", r.ID, r.ExecID)

	var p *process
	var err error
	if p, err = s.getProcess(r.ID, r.ExecID); err != nil {
		return nil, err
	}

	// This is a container
	if p.cid == p.id {
		rhs := s.newRunhcs()
		dopts := &runhcs.DeleteOpts{
			Force: true,
		}
		if err := rhs.Delete(ctx, p.id, dopts); err != nil {
			return nil, errors.Wrapf(err, "failed to delete container: %s", p.id)
		}
	}

	select {
	case <-time.After(5 * time.Second):
		// Force close the container process since it didn't shutdown in time.
		p.close()
	case <-p.waitBlock:
	}

	exit := p.stat()

	s.mu.Lock()
	delete(s.processes, p.id)
	s.mu.Unlock()

	s.publisher.Publish(ctx, runtime.TaskDeleteEventTopic, &eventstypes.TaskDelete{
		ContainerID: p.id,
		Pid:         exit.pid,
		ExitStatus:  exit.exitStatus,
		ExitedAt:    exit.exitedAt,
	})

	return &taskAPI.DeleteResponse{
		ExitedAt:   exit.exitedAt,
		ExitStatus: exit.exitStatus,
		Pid:        exit.pid,
	}, nil
}

// Pids returns all pids inside the container
func (s *service) Pids(ctx context.Context, r *taskAPI.PidsRequest) (*taskAPI.PidsResponse, error) {
	log.G(ctx).Debugf("Pids: %s", r.ID)

	return nil, errdefs.ErrNotImplemented
}

// Pause the container
func (s *service) Pause(ctx context.Context, r *taskAPI.PauseRequest) (*ptypes.Empty, error) {
	log.G(ctx).Debugf("Pause: %s", r.ID)

	// TODO: Validate that 'id' is actually a valid parent container ID
	var p *process
	var err error
	if p, err = s.getProcess(r.ID, ""); err != nil {
		return nil, err
	}

	rhcs := s.newRunhcs()
	if err = rhcs.Pause(ctx, p.id); err != nil {
		return nil, err
	}

	s.publisher.Publish(ctx, runtime.TaskPausedEventTopic, &eventstypes.TaskPaused{
		r.ID,
	})

	return empty, nil
}

// Resume the container
func (s *service) Resume(ctx context.Context, r *taskAPI.ResumeRequest) (*ptypes.Empty, error) {
	log.G(ctx).Debugf("Resume: %s", r.ID)

	// TODO: Validate that 'id' is actually a valid parent container ID
	var p *process
	var err error
	if p, err = s.getProcess(r.ID, ""); err != nil {
		return nil, err
	}

	rhcs := s.newRunhcs()
	if err = rhcs.Resume(ctx, p.id); err != nil {
		return nil, err
	}

	s.publisher.Publish(ctx, runtime.TaskResumedEventTopic, &eventstypes.TaskResumed{
		r.ID,
	})

	return empty, nil
}

// Checkpoint the container
func (s *service) Checkpoint(ctx context.Context, r *taskAPI.CheckpointTaskRequest) (*ptypes.Empty, error) {
	log.G(ctx).Debugf("Checkpoint: %s", r.ID)

	return nil, errdefs.ErrNotImplemented
}

// Kill a process with the provided signal
func (s *service) Kill(ctx context.Context, r *taskAPI.KillRequest) (*ptypes.Empty, error) {
	log.G(ctx).Debugf("Kill: %s: %s", r.ID, r.ExecID)

	var p *process
	var err error
	if p, err = s.getProcess(r.ID, r.ExecID); err != nil {
		return nil, err
	}

	// TODO: JTERRY75 runhcs support for r.All?
	rhcs := s.newRunhcs()
	if err = rhcs.Kill(ctx, p.id, strconv.FormatUint(uint64(r.Signal), 10)); err != nil {
		if !strings.Contains(err.Error(), "container is stopped") {
			return nil, err
		}
	}

	return empty, nil
}

// Exec an additional process inside the container
func (s *service) Exec(ctx context.Context, r *taskAPI.ExecProcessRequest) (*ptypes.Empty, error) {
	log.G(ctx).Debugf("Exec: %s: %s", r.ID, r.ExecID)

	s.mu.Lock()
	defer s.mu.Unlock()

	var parent *process
	var err error
	// Get the parent container
	if parent, err = s.getProcessLocked(r.ID, ""); err != nil {
		return nil, err
	}

	if p := s.processes[r.ExecID]; p != nil {
		return nil, errdefs.ToGRPCf(errdefs.ErrAlreadyExists, "exec process %s already exists", r.ExecID)
	}

	execFmt := fmt.Sprintf("exec-%s-%s", r.ID, r.ExecID)

	var spec oci.Process
	if err := json.Unmarshal(r.Spec.Value, &spec); err != nil {
		return nil, errors.Wrap(err, "request.Spec was not oci process")
	}
	procConfig := path.Join(parent.bundle, execFmt+"config.json")
	cf, err := os.OpenFile(procConfig, os.O_CREATE|os.O_WRONLY, 0)
	if err != nil {
		return nil, errors.Wrap(err, "bundle does not contain config.json")
	}
	if err := json.NewEncoder(cf).Encode(spec); err != nil {
		cf.Close()
		return nil, errors.Wrap(err, "failed to write Mounts into config.json")
	}
	cf.Close()

	// Create the IO for the process
	io, err := runc.NewPipeIO()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create io pipes")
	}
	defer func() {
		if err != nil {
			io.Close()
		}
	}()
	// Create the upstream IO
	ps, err := newPipeSet(ctx, r.Stdin, r.Stdout, r.Stderr, r.Terminal)
	if err != nil {
		return nil, errors.Wrap(err, "failed to connect to upstream IO")
	}
	defer func() {
		if err != nil {
			ps.Close()
		}
	}()
	// Create the relay for them both.
	pr := newPipeRelay(ctx, ps, io)
	defer func() {
		if err != nil {
			pr.close()
		}
	}()

	process, err := newExecProcess(
		ctx,
		s,
		r.ID,
		r.ExecID,
		pr,
		parent.bundle,
		r.Stdin,
		r.Stdout,
		r.Stderr,
		r.Terminal)

	if err != nil {
		return nil, err
	}
	s.processes[r.ExecID] = process

	s.publisher.Publish(ctx,
		runtime.TaskExecAddedEventTopic,
		&eventstypes.TaskExecAdded{
			ContainerID: process.cid,
			ExecID:      process.id,
		})

	return empty, nil
}

// ResizePty of a process
func (s *service) ResizePty(ctx context.Context, r *taskAPI.ResizePtyRequest) (*ptypes.Empty, error) {
	log.G(ctx).Debugf("ResizePty: %s: %s", r.ID, r.ExecID)

	var p *process
	var err error
	if p, err = s.getProcess(r.ID, r.ExecID); err != nil {
		return nil, err
	}

	pid := int(p.stat().pid)
	opts := runhcs.ResizeTTYOpts{
		Pid: &pid,
	}
	rhcs := s.newRunhcs()
	if err = rhcs.ResizeTTY(ctx, p.cid, uint16(r.Width), uint16(r.Height), &opts); err != nil {
		return nil, err
	}

	return empty, nil
}

// CloseIO of a process
func (s *service) CloseIO(ctx context.Context, r *taskAPI.CloseIORequest) (*ptypes.Empty, error) {
	log.G(ctx).Debugf("CloseIO: %s: %s", r.ID, r.ExecID)

	var p *process
	var err error
	if p, err = s.getProcess(r.ID, r.ExecID); err != nil {
		return nil, err
	}
	p.closeIO()
	return empty, nil
}

// Update a running container
func (s *service) Update(ctx context.Context, r *taskAPI.UpdateTaskRequest) (*ptypes.Empty, error) {
	log.G(ctx).Debugf("Update: %s", r.ID)

	return nil, errdefs.ErrNotImplemented
}

// Wait for a process to exit
func (s *service) Wait(ctx context.Context, r *taskAPI.WaitRequest) (*taskAPI.WaitResponse, error) {
	log.G(ctx).Debugf("Wait: %s: %s", r.ID, r.ExecID)

	var p *process
	var err error
	if p, err = s.getProcess(r.ID, r.ExecID); err != nil {
		return nil, err
	}
	pe := p.wait()
	return &taskAPI.WaitResponse{
		ExitedAt:   pe.exitedAt,
		ExitStatus: pe.exitStatus,
	}, nil
}

// Stats returns statistics about the running container
func (s *service) Stats(ctx context.Context, r *taskAPI.StatsRequest) (*taskAPI.StatsResponse, error) {
	log.G(ctx).Debugf("Stats: %s", r.ID)

	return nil, errdefs.ErrNotImplemented
}

// Connect returns the runhcs shim information
func (s *service) Connect(ctx context.Context, r *taskAPI.ConnectRequest) (*taskAPI.ConnectResponse, error) {
	log.G(ctx).Debugf("Connect: %s", r.ID)

	var taskpid uint32
	p, _ := s.getProcess(r.ID, "")
	if p != nil {
		taskpid = p.stat().pid
	}

	return &taskAPI.ConnectResponse{
		ShimPid: uint32(os.Getpid()),
		TaskPid: taskpid,
		Version: runhcsShimVersion,
	}, nil
}

// Shutdown stops this instance of the runhcs shim
func (s *service) Shutdown(ctx context.Context, r *taskAPI.ShutdownRequest) (*ptypes.Empty, error) {
	log.G(ctx).Debugf("Shutdown: %s", r.ID)

	if s.debugListener != nil {
		s.debugListener.Close()
	}

	os.Exit(0)
	return empty, nil
}
