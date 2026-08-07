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

package sandbox

import (
	"context"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	bootapi "github.com/containerd/containerd/api/runtime/bootstrap/v1"
	sandboxapi "github.com/containerd/containerd/api/runtime/sandbox/v1"
	taskAPI "github.com/containerd/containerd/api/runtime/task/v2"
	typesapi "github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/v2/pkg/protobuf"
	"github.com/containerd/containerd/v2/pkg/shim"
	"github.com/containerd/containerd/v2/pkg/shutdown"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/errdefs"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/containerd/ttrpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.TTRPCPlugin,
		ID:   "task",
		Requires: []plugin.Type{
			plugins.EventPlugin,
			plugins.InternalPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (any, error) {
			ss, err := ic.GetByID(plugins.InternalPlugin, "shutdown")
			if err != nil {
				return nil, err
			}
			return newService(ss.(shutdown.Service)), nil
		},
	})
}

func NewManager(name string) shim.Shim {
	return manager{name: name}
}

type manager struct {
	name string
}

func (m manager) Name() string {
	return m.name
}

func (m manager) Start(ctx context.Context, opts *bootapi.BootstrapParams) (*bootapi.BootstrapResult, error) {
	return nil, errdefs.ErrNotImplemented
}

func (m manager) Stop(ctx context.Context, id string) (shim.StopStatus, error) {
	return shim.StopStatus{}, errdefs.ErrNotImplemented
}

func (m manager) Info(ctx context.Context, optionsR io.Reader) (*typesapi.RuntimeInfo, error) {
	info := &typesapi.RuntimeInfo{
		Name: "io.containerd.example.sandbox.v1",
		Version: &typesapi.RuntimeVersion{
			Version: "v1.0.0",
		},
	}
	return info, nil
}

func newService(sd shutdown.Service) *exampleService {
	return &exampleService{
		shutdown:  sd,
		sandboxes: make(map[string]*sandboxState),
	}
}

var (
	_ = shim.TTRPCService(&exampleService{})
	_ = sandboxapi.TTRPCSandboxService(&exampleService{})
	_ = taskAPI.TTRPCTaskService(&exampleService{})
)

type sandboxState struct {
	id          string
	bundlePath  string
	netnsPath   string
	annotations map[string]string
	pid         uint32
	state       string
	createdAt   time.Time
	exitedAt    time.Time
	exitStatus  uint32
	waitCh      chan struct{}
}

type exampleService struct {
	shutdown shutdown.Service

	mu        sync.Mutex
	sandboxes map[string]*sandboxState
}

func (s *exampleService) RegisterTTRPC(server *ttrpc.Server) error {
	taskAPI.RegisterTTRPCTaskService(server, s)
	sandboxapi.RegisterTTRPCSandboxService(server, s)
	return nil
}

func (s *exampleService) CreateSandbox(ctx context.Context, req *sandboxapi.CreateSandboxRequest) (*sandboxapi.CreateSandboxResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sandboxes[req.GetSandboxID()]; ok {
		return nil, errdefs.ErrAlreadyExists
	}

	s.sandboxes[req.GetSandboxID()] = &sandboxState{
		id:          req.GetSandboxID(),
		bundlePath:  req.GetBundlePath(),
		netnsPath:   req.GetNetnsPath(),
		annotations: req.GetAnnotations(),
		state:       "CREATED",
		waitCh:      make(chan struct{}),
	}
	return &sandboxapi.CreateSandboxResponse{}, nil
}

func (s *exampleService) StartSandbox(ctx context.Context, req *sandboxapi.StartSandboxRequest) (*sandboxapi.StartSandboxResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sb, err := s.getSandbox(req.GetSandboxID())
	if err != nil {
		return nil, err
	}

	if sb.createdAt.IsZero() {
		sb.createdAt = time.Now().UTC()
	}
	sb.pid = uint32(os.Getpid())
	sb.state = "RUNNING"

	return &sandboxapi.StartSandboxResponse{
		Pid:       sb.pid,
		CreatedAt: protobuf.ToTimestamp(sb.createdAt),
	}, nil
}

func (s *exampleService) Platform(ctx context.Context, req *sandboxapi.PlatformRequest) (*sandboxapi.PlatformResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.getSandbox(req.GetSandboxID()); err != nil {
		return nil, err
	}

	return &sandboxapi.PlatformResponse{
		Platform: &typesapi.Platform{
			OS:           runtime.GOOS,
			Architecture: runtime.GOARCH,
		},
	}, nil
}

func (s *exampleService) StopSandbox(ctx context.Context, req *sandboxapi.StopSandboxRequest) (*sandboxapi.StopSandboxResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sb, err := s.getSandbox(req.GetSandboxID())
	if err != nil {
		return nil, err
	}

	if sb.state != "STOPPED" {
		sb.state = "STOPPED"
		sb.exitedAt = time.Now().UTC()
		close(sb.waitCh)
	}

	return &sandboxapi.StopSandboxResponse{}, nil
}

func (s *exampleService) WaitSandbox(ctx context.Context, req *sandboxapi.WaitSandboxRequest) (*sandboxapi.WaitSandboxResponse, error) {
	s.mu.Lock()
	sb, err := s.getSandbox(req.GetSandboxID())
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	waitCh := sb.waitCh
	s.mu.Unlock()

	select {
	case <-waitCh:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sb, err = s.getSandbox(req.GetSandboxID())
	if err != nil {
		return nil, err
	}

	return &sandboxapi.WaitSandboxResponse{
		ExitStatus: sb.exitStatus,
		ExitedAt:   protobuf.ToTimestamp(sb.exitedAt),
	}, nil
}

func (s *exampleService) SandboxStatus(ctx context.Context, req *sandboxapi.SandboxStatusRequest) (*sandboxapi.SandboxStatusResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sb, err := s.getSandbox(req.GetSandboxID())
	if err != nil {
		return nil, err
	}

	info := map[string]string{
		"bundle": sb.bundlePath,
		"netns":  sb.netnsPath,
	}
	if req.GetVerbose() {
		for k, v := range sb.annotations {
			info["annotation."+k] = v
		}
	}

	return &sandboxapi.SandboxStatusResponse{
		SandboxID: sb.id,
		Pid:       sb.pid,
		State:     sb.state,
		Info:      info,
		CreatedAt: protobuf.ToTimestamp(sb.createdAt),
		ExitedAt:  protobuf.ToTimestamp(sb.exitedAt),
	}, nil
}

func (s *exampleService) PingSandbox(ctx context.Context, req *sandboxapi.PingRequest) (*sandboxapi.PingResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.getSandbox(req.GetSandboxID()); err != nil {
		return nil, err
	}
	return &sandboxapi.PingResponse{}, nil
}

func (s *exampleService) ShutdownSandbox(ctx context.Context, req *sandboxapi.ShutdownSandboxRequest) (*sandboxapi.ShutdownSandboxResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.getSandbox(req.GetSandboxID()); err != nil {
		return nil, err
	}
	delete(s.sandboxes, req.GetSandboxID())
	go s.shutdown.Shutdown()
	return &sandboxapi.ShutdownSandboxResponse{}, nil
}

func (s *exampleService) SandboxMetrics(ctx context.Context, req *sandboxapi.SandboxMetricsRequest) (*sandboxapi.SandboxMetricsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.getSandbox(req.GetSandboxID()); err != nil {
		return nil, err
	}
	return nil, errdefs.ErrNotImplemented
}

func (s *exampleService) Create(ctx context.Context, r *taskAPI.CreateTaskRequest) (*taskAPI.CreateTaskResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *exampleService) Start(ctx context.Context, r *taskAPI.StartRequest) (*taskAPI.StartResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *exampleService) Delete(ctx context.Context, r *taskAPI.DeleteRequest) (*taskAPI.DeleteResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *exampleService) Exec(ctx context.Context, r *taskAPI.ExecProcessRequest) (*emptypb.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *exampleService) ResizePty(ctx context.Context, r *taskAPI.ResizePtyRequest) (*emptypb.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *exampleService) State(ctx context.Context, r *taskAPI.StateRequest) (*taskAPI.StateResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *exampleService) Pause(ctx context.Context, r *taskAPI.PauseRequest) (*emptypb.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *exampleService) Resume(ctx context.Context, r *taskAPI.ResumeRequest) (*emptypb.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *exampleService) Kill(ctx context.Context, r *taskAPI.KillRequest) (*emptypb.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *exampleService) Pids(ctx context.Context, r *taskAPI.PidsRequest) (*taskAPI.PidsResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *exampleService) CloseIO(ctx context.Context, r *taskAPI.CloseIORequest) (*emptypb.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *exampleService) Checkpoint(ctx context.Context, r *taskAPI.CheckpointTaskRequest) (*emptypb.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *exampleService) Connect(ctx context.Context, r *taskAPI.ConnectRequest) (*taskAPI.ConnectResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *exampleService) Shutdown(ctx context.Context, r *taskAPI.ShutdownRequest) (*emptypb.Empty, error) {
	go s.shutdown.Shutdown()
	return &emptypb.Empty{}, nil
}

func (s *exampleService) Stats(ctx context.Context, r *taskAPI.StatsRequest) (*taskAPI.StatsResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *exampleService) Update(ctx context.Context, r *taskAPI.UpdateTaskRequest) (*emptypb.Empty, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *exampleService) Wait(ctx context.Context, r *taskAPI.WaitRequest) (*taskAPI.WaitResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *exampleService) getSandbox(id string) (*sandboxState, error) {
	sb, ok := s.sandboxes[id]
	if !ok {
		return nil, errdefs.ErrNotFound
	}
	return sb, nil
}
