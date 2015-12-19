package server

import (
	"errors"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd/api/grpc/types"
	"github.com/docker/containerd/runtime"
	"github.com/docker/containerd/supervisor"
	"github.com/opencontainers/specs"
	"golang.org/x/net/context"
)

type apiServer struct {
	sv *supervisor.Supervisor
}

// NewServer returns grpc server instance
func NewServer(sv *supervisor.Supervisor) types.APIServer {
	return &apiServer{
		sv: sv,
	}
}

func (s *apiServer) CreateContainer(ctx context.Context, c *types.CreateContainerRequest) (*types.CreateContainerResponse, error) {
	if c.BundlePath == "" {
		return nil, errors.New("empty bundle path")
	}
	e := supervisor.NewEvent(types.EventType_EVENT_TYPE_START_CONTAINER)
	e.ID = c.Id
	e.BundlePath = c.BundlePath
	e.Stdout = c.Stdout
	e.Stderr = c.Stderr
	e.Stdin = c.Stdin
	e.Console = c.Console
	e.StartResponse = make(chan supervisor.StartResponse, 1)
	if c.Checkpoint != "" {
		e.Checkpoint = &runtime.Checkpoint{
			Name: c.Checkpoint,
		}
	}
	s.sv.SendEvent(e)
	if err := <-e.Err; err != nil {
		return nil, err
	}
	sr := <-e.StartResponse
	return &types.CreateContainerResponse{
		Pid: uint32(sr.Pid),
	}, nil
}

func (s *apiServer) Signal(ctx context.Context, r *types.SignalRequest) (*types.SignalResponse, error) {
	e := supervisor.NewEvent(types.EventType_EVENT_TYPE_SIGNAL)
	e.ID = r.Id
	e.Pid = int(r.Pid)
	e.Signal = syscall.Signal(int(r.Signal))
	s.sv.SendEvent(e)
	if err := <-e.Err; err != nil {
		return nil, err
	}
	return &types.SignalResponse{}, nil
}

func (s *apiServer) AddProcess(ctx context.Context, r *types.AddProcessRequest) (*types.AddProcessResponse, error) {
	process := &specs.Process{
		Terminal: r.Terminal,
		Args:     r.Args,
		Env:      r.Env,
		Cwd:      r.Cwd,
		User: specs.User{
			UID:            r.User.Uid,
			GID:            r.User.Gid,
			AdditionalGids: r.User.AdditionalGids,
		},
	}
	e := supervisor.NewEvent(types.EventType_EVENT_TYPE_ADD_PROCESS)
	e.ID = r.Id
	e.Process = process
	e.Console = r.Console
	e.Stdin = r.Stdin
	e.Stdout = r.Stdout
	e.Stderr = r.Stderr
	s.sv.SendEvent(e)
	if err := <-e.Err; err != nil {
		return nil, err
	}
	return &types.AddProcessResponse{Pid: uint32(e.Pid)}, nil
}

func (s *apiServer) CreateCheckpoint(ctx context.Context, r *types.CreateCheckpointRequest) (*types.CreateCheckpointResponse, error) {
	e := supervisor.NewEvent(types.EventType_EVENT_TYPE_CREATE_CHECKPOINT)
	e.ID = r.Id
	e.Checkpoint = &runtime.Checkpoint{
		Name:        r.Checkpoint.Name,
		Exit:        r.Checkpoint.Exit,
		Tcp:         r.Checkpoint.Tcp,
		UnixSockets: r.Checkpoint.UnixSockets,
		Shell:       r.Checkpoint.Shell,
	}
	s.sv.SendEvent(e)
	if err := <-e.Err; err != nil {
		return nil, err
	}
	return &types.CreateCheckpointResponse{}, nil
}

func (s *apiServer) DeleteCheckpoint(ctx context.Context, r *types.DeleteCheckpointRequest) (*types.DeleteCheckpointResponse, error) {
	if r.Name == "" {
		return nil, errors.New("checkpoint name cannot be empty")
	}
	e := supervisor.NewEvent(types.EventType_EVENT_TYPE_DELETE_CHECKPOINT)
	e.ID = r.Id
	e.Checkpoint = &runtime.Checkpoint{
		Name: r.Name,
	}
	s.sv.SendEvent(e)
	if err := <-e.Err; err != nil {
		return nil, err
	}
	return &types.DeleteCheckpointResponse{}, nil
}

func (s *apiServer) ListCheckpoint(ctx context.Context, r *types.ListCheckpointRequest) (*types.ListCheckpointResponse, error) {
	e := supervisor.NewEvent(types.EventType_EVENT_TYPE_GET_CONTAINER)
	s.sv.SendEvent(e)
	if err := <-e.Err; err != nil {
		return nil, err
	}
	var container runtime.Container
	for _, c := range e.Containers {
		if c.ID() == r.Id {
			container = c
			break
		}
	}
	if container == nil {
		return nil, grpc.Errorf(codes.NotFound, "no such containers")
	}
	checkpoints, err := container.Checkpoints()
	if err != nil {
		return nil, err
	}
	var out []*types.Checkpoint
	for _, c := range checkpoints {
		out = append(out, &types.Checkpoint{
			Name:        c.Name,
			Tcp:         c.Tcp,
			Shell:       c.Shell,
			UnixSockets: c.UnixSockets,
			// TODO: figure out timestamp
			//Timestamp:   c.Timestamp,
		})
	}
	return &types.ListCheckpointResponse{Checkpoints: out}, nil
}

func (s *apiServer) State(ctx context.Context, r *types.StateRequest) (*types.StateResponse, error) {
	e := supervisor.NewEvent(types.EventType_EVENT_TYPE_GET_CONTAINER)
	s.sv.SendEvent(e)
	if err := <-e.Err; err != nil {
		return nil, err
	}
	m := s.sv.Machine()
	state := &types.StateResponse{
		Machine: &types.Machine{
			Id:     m.ID,
			Cpus:   uint32(m.Cpus),
			Memory: uint64(m.Cpus),
		},
	}
	for _, c := range e.Containers {
		processes, err := c.Processes()
		if err != nil {
			return nil, grpc.Errorf(codes.Internal, "get processes for container")
		}
		var procs []*types.Process
		for _, p := range processes {
			pid, err := p.Pid()
			if err != nil {
				logrus.WithField("error", err).Error("get process pid")
			}
			oldProc := p.Spec()
			procs = append(procs, &types.Process{
				Pid:      uint32(pid),
				Terminal: oldProc.Terminal,
				Args:     oldProc.Args,
				Env:      oldProc.Env,
				Cwd:      oldProc.Cwd,
				User: &types.User{
					Uid:            oldProc.User.UID,
					Gid:            oldProc.User.GID,
					AdditionalGids: oldProc.User.AdditionalGids,
				},
			})
		}
		state.Containers = append(state.Containers, &types.Container{
			Id:         c.ID(),
			BundlePath: c.Path(),
			Processes:  procs,
			Status:     string(c.State().Status),
		})
	}
	return state, nil
}

func (s *apiServer) UpdateContainer(ctx context.Context, r *types.UpdateContainerRequest) (*types.UpdateContainerResponse, error) {
	e := supervisor.NewEvent(types.EventType_EVENT_TYPE_UPDATE_CONTAINER)
	e.ID = r.Id
	if r.Signal != 0 {
		e.Signal = syscall.Signal(r.Signal)
	}
	e.State = &runtime.State{
		Status: runtime.Status(r.Status),
	}
	s.sv.SendEvent(e)
	if err := <-e.Err; err != nil {
		return nil, err
	}
	return &types.UpdateContainerResponse{}, nil
}

func (s *apiServer) Events(r *types.EventsRequest, stream types.API_EventsServer) error {
	events := s.sv.Events()
	defer s.sv.Unsubscribe(events)
	for evt := range events {
		var ev *types.Event
		switch evt.Type {
		case types.EventType_EVENT_TYPE_EXIT, types.EventType_EVENT_TYPE_EXEC_EXIT:
			ev = &types.Event{
				Type:   "exit",
				Id:     evt.ID,
				Pid:    uint32(evt.Pid),
				Status: uint32(evt.Status),
			}
		case types.EventType_EVENT_TYPE_OOM:
			ev = &types.Event{
				Type: "oom",
				Id:   evt.ID,
			}
		}
		if ev != nil {
			if err := stream.Send(ev); err != nil {
				return err
			}
		}

	}
	return nil
}

func (s *apiServer) GetStats(r *types.StatsRequest, stream types.API_GetStatsServer) error {
	e := supervisor.NewEvent(types.EventType_EVENT_TYPE_STATS)
	e.ID = r.Id
	s.sv.SendEvent(e)
	if err := <-e.Err; err != nil {
		if err == supervisor.ErrContainerNotFound {
			return grpc.Errorf(codes.NotFound, err.Error())
		}
		return err
	}
	defer func() {
		ue := supervisor.NewEvent(types.EventType_EVENT_TYPE_UNSUBSCRIBE_STATS)
		ue.ID = e.ID
		ue.Stats = e.Stats
		s.sv.SendEvent(ue)
		if err := <-ue.Err; err != nil {
			logrus.Errorf("Error unsubscribing %s: %v", r.Id, err)
		}
	}()
	for {
		select {
		case st := <-e.Stats:
			pbSt, ok := st.(*types.Stats)
			if !ok {
				panic("invalid stats type from collector")
			}
			if err := stream.Send(pbSt); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return nil
		}
	}
	return nil
}
