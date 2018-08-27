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
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Microsoft/hcsshim/cmd/go-runhcs"
	containerd_types "github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/api/types/task"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/runtime/v2/shim"
	taskAPI "github.com/containerd/containerd/runtime/v2/task"
	"github.com/containerd/go-runc"
	ptypes "github.com/gogo/protobuf/types"
	oci "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	runhcsBinary      = "runhcs"
	runhcsVersion     = "0.0.1"
	runhcsDebugLegacy = "--debug" // TODO: JTERRY75 remove when all cmd's are complete in go-runhcs
)

var (
	empty = &ptypes.Empty{}
)

func newRunhcs(bundle string) *runhcs.Runhcs {
	rhs := &runhcs.Runhcs{
		Debug:     logrus.GetLevel() == logrus.DebugLevel,
		LogFormat: runhcs.JSON,
		Owner:     "containerd-runhcs-shim-v1",
	}
	if rhs.Debug {
		rhs.Log = filepath.Join(bundle, "runhcs-debug.log")
	}
	return rhs
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

	id        string
	processes map[string]*process

	publisher events.Publisher
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
	path, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(path); err != nil {
		return nil, err
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
		"-debug",
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
	s.mu.Lock()
	defer s.mu.Unlock()

	var p *process
	var err error
	if p, err = s.getProcessLocked(r.ID, r.ExecID); err != nil {
		return nil, err
	}

	cmd := exec.Command(runhcsBinary, runhcsDebugLegacy, "state", p.id)
	sout := getBuffer()
	defer putBuffer(sout)

	cmd.Stdout = sout
	_, stateErr := runCmd(ctx, cmd)
	if stateErr != nil {
		return nil, stateErr
	}

	// TODO: JTERRY75 merge this with runhcs declaration
	type containerState struct {
		// Version is the OCI version for the container
		Version string `json:"ociVersion"`
		// ID is the container ID
		ID string `json:"id"`
		// InitProcessPid is the init process id in the parent namespace
		InitProcessPid int `json:"pid"`
		// Status is the current status of the container, running, paused, ...
		Status string `json:"status"`
		// Bundle is the path on the filesystem to the bundle
		Bundle string `json:"bundle"`
		// Rootfs is a path to a directory containing the container's root filesystem.
		Rootfs string `json:"rootfs"`
		// Created is the unix timestamp for the creation time of the container in UTC
		Created time.Time `json:"created"`
		// Annotations is the user defined annotations added to the config.
		Annotations map[string]string `json:"annotations,omitempty"`
		// The owner of the state directory (the owner of the container).
		Owner string `json:"owner"`
	}

	var cs containerState
	if err := json.NewDecoder(sout).Decode(&cs); err != nil {
		log.G(ctx).WithError(err).Debugf("failed to decode runhcs state output: %s", sout.Bytes())
		return nil, err
	}

	status := task.StatusUnknown
	switch cs.Status {
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
		Bundle:     cs.Bundle,
		Pid:        uint32(cs.InitProcessPid),
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
	if len(mounts) != 1 {
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
	}

	cf, err := os.OpenFile(path.Join(bundle, "config.json"), os.O_RDWR, 0)
	if err != nil {
		return errors.Wrap(err, "bundle does not contain config.json")
	}
	defer cf.Close()
	var spec oci.Spec
	if err := json.NewDecoder(cf).Decode(&spec); err != nil {
		return errors.Wrap(err, "bundle config.json is not valid oci spec")
	}
	if err := cf.Truncate(0); err != nil {
		return errors.Wrap(err, "failed to truncate config.json")
	}
	if _, err := cf.Seek(0, 0); err != nil {
		return errors.Wrap(err, "failed to seek to 0 in config.json")
	}

	// Append the parents
	spec.Windows.LayerFolders = append(spec.Windows.LayerFolders, parentLayerPaths...)
	// Append the scratch
	spec.Windows.LayerFolders = append(spec.Windows.LayerFolders, m.Source)

	if err := json.NewEncoder(cf).Encode(spec); err != nil {
		return errors.Wrap(err, "failed to write Mounts into config.json")
	}
	return nil
}

// Create a new initial process and container with runhcs
func (s *service) Create(ctx context.Context, r *taskAPI.CreateTaskRequest) (*taskAPI.CreateTaskResponse, error) {
	log.G(ctx).Infof("Create: %s", r.ID)

	// Hold the lock for the entire duration to avoid duplicate process creation.
	s.mu.Lock()
	defer s.mu.Unlock()

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

	pidfilePath := path.Join(r.Bundle, "runhcs-pidfile.pid")
	copts := &runhcs.CreateOpts{
		IO:      io,
		PidFile: pidfilePath,
		ShimLog: path.Join(r.Bundle, "runhcs-shim.log"),
		VMLog:   path.Join(r.Bundle, "runhcs-vm-shim.log"),
	}

	rhcs := newRunhcs(r.Bundle)
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

	return &taskAPI.CreateTaskResponse{
		Pid: uint32(pid),
	}, nil
}

// Start a process
func (s *service) Start(ctx context.Context, r *taskAPI.StartRequest) (*taskAPI.StartResponse, error) {
	log.G(ctx).Infof("Start: %s: %s", r.ID, r.ExecID)
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

	rhcs := newRunhcs(p.bundle)

	// This is a start/exec
	if r.ExecID != "" {
		execFmt := fmt.Sprintf("exec-%s-%s", r.ID, r.ExecID)
		pidfilePath := path.Join(p.bundle, fmt.Sprintf("runhcs-%s-pidfile.pid", execFmt))
		procConfig := path.Join(p.bundle, execFmt+"config.json")
		eopts := &runhcs.ExecOpts{
			IO:      p.relay.io,
			PidFile: pidfilePath,
			ShimLog: path.Join(p.bundle, fmt.Sprintf("runhcs-%s-shim.log", execFmt)),
			Detach:  true,
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
		p.pid = uint32(pid)
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
	return &taskAPI.StartResponse{
		Pid: p.pid,
	}, nil
}

// Delete the initial process and container
func (s *service) Delete(ctx context.Context, r *taskAPI.DeleteRequest) (*taskAPI.DeleteResponse, error) {
	log.G(ctx).Infof("Delete: %s: %s", r.ID, r.ExecID)

	var p *process
	var err error
	if p, err = s.getProcess(r.ID, r.ExecID); err != nil {
		return nil, err
	}

	rhs := newRunhcs(p.bundle)

	dopts := &runhcs.DeleteOpts{
		Force: true,
	}
	if err := rhs.Delete(ctx, p.id, dopts); err != nil {
		return nil, errors.Wrapf(err, "failed to delete container: %s", p.id)
	}

	select {
	case <-time.After(5 * time.Second):
		// Force close the container process since it didnt shutdown in time.
		p.close()
	case <-p.waitBlock:
	}

	exit := p.stat()

	s.mu.Lock()
	delete(s.processes, p.id)
	s.mu.Unlock()
	return &taskAPI.DeleteResponse{
		ExitedAt:   exit.exitedAt,
		ExitStatus: exit.exitStatus,
		Pid:        p.pid,
	}, nil
}

// Pids returns all pids inside the container
func (s *service) Pids(ctx context.Context, r *taskAPI.PidsRequest) (*taskAPI.PidsResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

// Pause the container
func (s *service) Pause(ctx context.Context, r *taskAPI.PauseRequest) (*ptypes.Empty, error) {
	// TODO: Validate that 'id' is actually a valid parent container ID
	if _, err := s.getProcess(r.ID, ""); err != nil {
		return nil, err
	}

	cmd := exec.Command(runhcsBinary, runhcsDebugLegacy, "pause", r.ID)
	_, err := runCmd(ctx, cmd)
	if err != nil {
		return nil, err
	}

	return empty, nil
}

// Resume the container
func (s *service) Resume(ctx context.Context, r *taskAPI.ResumeRequest) (*ptypes.Empty, error) {
	// TODO: Validate that 'id' is actually a valid parent container ID
	if _, err := s.getProcess(r.ID, ""); err != nil {
		return nil, err
	}

	cmd := exec.Command(runhcsBinary, runhcsDebugLegacy, "resume", r.ID)
	_, err := runCmd(ctx, cmd)
	if err != nil {
		return nil, err
	}

	return empty, nil
}

// Checkpoint the container
func (s *service) Checkpoint(ctx context.Context, r *taskAPI.CheckpointTaskRequest) (*ptypes.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

// Kill a process with the provided signal
func (s *service) Kill(ctx context.Context, r *taskAPI.KillRequest) (*ptypes.Empty, error) {
	log.G(ctx).Infof("Kill: %s: %s", r.ID, r.ExecID)

	var p *process
	var err error
	if p, err = s.getProcess(r.ID, r.ExecID); err != nil {
		return nil, err
	}

	// TODO: JTERRY75 runhcs needs r.Signal in string form
	// TODO: JTERRY75 runhcs support for r.All?
	rhcs := newRunhcs(p.bundle)
	if err = rhcs.Kill(ctx, p.id, strconv.FormatUint(uint64(r.Signal), 10)); err != nil {
		if !strings.Contains(err.Error(), "container is not running") {
			return nil, err
		}
	}

	return empty, nil
}

// Exec an additional process inside the container
func (s *service) Exec(ctx context.Context, r *taskAPI.ExecProcessRequest) (*ptypes.Empty, error) {
	log.G(ctx).Infof("Exec: %s: %s", r.ID, r.ExecID)

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

	return empty, nil
}

// ResizePty of a process
func (s *service) ResizePty(ctx context.Context, r *taskAPI.ResizePtyRequest) (*ptypes.Empty, error) {
	var p *process
	var err error
	if p, err = s.getProcess(r.ID, r.ExecID); err != nil {
		return nil, err
	}

	cmd := exec.Command(
		runhcsBinary,
		runhcsDebugLegacy,
		"resize-tty",
		p.cid,
		"-p",
		strconv.FormatUint(uint64(p.pid), 10),
		strconv.FormatUint(uint64(r.Width), 10),
		strconv.FormatUint(uint64(r.Height), 10))

	_, err = runCmd(ctx, cmd)
	if err != nil {
		return nil, err
	}

	return empty, nil
}

// CloseIO of a process
func (s *service) CloseIO(ctx context.Context, r *taskAPI.CloseIORequest) (*ptypes.Empty, error) {
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
	return nil, errdefs.ErrNotImplemented
}

// Wait for a process to exit
func (s *service) Wait(ctx context.Context, r *taskAPI.WaitRequest) (*taskAPI.WaitResponse, error) {
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
	return nil, errdefs.ErrNotImplemented
}

// Connect returns the runhcs shim information
func (s *service) Connect(ctx context.Context, r *taskAPI.ConnectRequest) (*taskAPI.ConnectResponse, error) {
	return &taskAPI.ConnectResponse{
		ShimPid: uint32(os.Getpid()),
		TaskPid: s.processes[s.id].pid,
		Version: runhcsVersion,
	}, nil
}

// Shutdown stops this instance of the runhcs shim
func (s *service) Shutdown(ctx context.Context, r *taskAPI.ShutdownRequest) (*ptypes.Empty, error) {
	log.G(ctx).Infof("Shutdown: %s", r.ID)

	os.Exit(0)
	return empty, nil
}
