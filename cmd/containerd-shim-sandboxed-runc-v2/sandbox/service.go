//go:build linux

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
	"fmt"
	"sync"
	"time"

	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2"
	api "github.com/containerd/containerd/api/runtime/sandbox/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errgrpc"
	"github.com/containerd/log"
	"github.com/containerd/platforms"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/containerd/ttrpc"
	"github.com/containerd/typeurl/v2"
	"github.com/moby/sys/userns"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"google.golang.org/protobuf/types/known/anypb"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/pkg/protobuf"
	"github.com/containerd/containerd/v2/pkg/shutdown"
	"github.com/containerd/containerd/v2/plugins"
)

// BinaryName is the name of this shim, used in messages. It serves the
// io.containerd.runc.v2 runtime type; a runtime handler reaches it through
// runtime_path.
const BinaryName = "containerd-shim-sandboxed-runc-v2"

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.TTRPCPlugin,
		ID:   "sandbox",
		Requires: []plugin.Type{
			plugins.InternalPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (any, error) {
			ss, err := ic.GetByID(plugins.InternalPlugin, "shutdown")
			if err != nil {
				return nil, err
			}
			return NewService(ss.(shutdown.Service)), nil
		},
	})
}

// Service implements the sandbox ttrpc service. containerd starts one shim per
// sandbox, so a Service hosts at most one.
type Service struct {
	shutdown shutdown.Service

	mu      sync.Mutex
	sandbox *instance
}

type instance struct {
	cfg       *Config
	bundle    string
	pins      Pins
	spec      *anypb.Any
	createdAt time.Time

	stopped  bool
	exitedAt time.Time
	// done is closed once the sandbox is stopped.
	done chan struct{}
}

var _ api.TTRPCSandboxService = (*Service)(nil)

// NewService returns a sandbox service. sd ends the shim on ShutdownSandbox.
func NewService(sd shutdown.Service) *Service {
	return &Service{shutdown: sd}
}

// RegisterTTRPC implements shim.TTRPCService.
func (s *Service) RegisterTTRPC(server *ttrpc.Server) error {
	api.RegisterTTRPCSandboxService(server, s)
	return nil
}

// findSandbox returns the hosted sandbox if it is the one asked for. The
// caller holds s.mu.
func (s *Service) findSandbox(id string) (*instance, error) {
	if s.sandbox == nil || s.sandbox.cfg.ID != id {
		return nil, fmt.Errorf("sandbox %q: %w", id, errdefs.ErrNotFound)
	}
	return s.sandbox, nil
}

// CreateSandbox sets the pod up: namespaces, hostname, sysctls and shared
// files. Nothing is left behind on failure.
func (s *Service) CreateSandbox(ctx context.Context, r *api.CreateSandboxRequest) (_ *api.CreateSandboxResponse, retErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sandbox != nil {
		return nil, errgrpc.ToGRPCf(errdefs.ErrAlreadyExists, "shim already hosts sandbox %q", s.sandbox.cfg.ID)
	}
	if userns.RunningInUserNS() {
		return nil, errgrpc.ToGRPCf(errdefs.ErrNotImplemented, "%s does not support rootless containerd", BinaryName)
	}
	bundle := r.GetBundlePath()
	if bundle == "" {
		return nil, errgrpc.ToGRPCf(errdefs.ErrInvalidArgument, "sandbox %q: no bundle path", r.GetSandboxID())
	}
	cfg, err := ConfigFromRequest(r)
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	log.G(ctx).WithField("id", cfg.ID).Warnf("%s is experimental: the pod sandbox runs without a pause container", BinaryName)
	if cfg.SharedPID {
		log.G(ctx).WithField("id", cfg.ID).Warnf("%s cannot share a pod PID namespace yet: the containers of this pod get their own PID namespaces", BinaryName)
	}

	defer func() {
		if retErr != nil {
			if err := Cleanup(ctx, bundle); err != nil {
				log.G(ctx).WithError(err).WithField("id", cfg.ID).Warn("failed to clean up after a failed sandbox creation")
			}
		}
	}()

	pins, err := setupNamespaces(bundle, cfg)
	if err != nil {
		return nil, errgrpc.ToGRPCf(err, "failed to set up the pod namespaces")
	}
	// TODO: SELinux. The shim allocates no pod process label and CRI does not
	// allocate one for a Sandbox API shim yet, so on an enforcing host the
	// containers of the pod without explicit SELinux options get independent
	// MCS labels and relabel the shared files and /dev/shm in turn, and the
	// pod-level SELinux options are not applied. Waits for
	// https://github.com/containerd/containerd/pull/14108, which allocates the
	// pod label in the CRI layer; the shim then applies the labels it is
	// handed to the files it creates.
	mounts, err := setupFiles(bundle, cfg)
	if err != nil {
		return nil, errgrpc.ToGRPCf(err, "failed to set up the pod files")
	}
	spec, err := typeurl.MarshalAnyToProto(buildSpec(cfg, pins, mounts))
	if err != nil {
		return nil, errgrpc.ToGRPCf(err, "failed to marshal the sandbox spec")
	}

	s.sandbox = &instance{
		cfg:       cfg,
		bundle:    bundle,
		pins:      pins,
		spec:      spec,
		createdAt: time.Now(),
		done:      make(chan struct{}),
	}
	return &api.CreateSandboxResponse{}, nil
}

// StartSandbox reports the sandbox as running. There is no process to start:
// the pid is 0 and the spec describes what the sandbox holds.
func (s *Service) StartSandbox(ctx context.Context, r *api.StartSandboxRequest) (*api.StartSandboxResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, err := s.findSandbox(r.GetSandboxID())
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	if inst.stopped {
		return nil, errgrpc.ToGRPCf(errdefs.ErrFailedPrecondition, "sandbox %q is stopped", inst.cfg.ID)
	}
	return &api.StartSandboxResponse{
		Pid:       0,
		CreatedAt: protobuf.ToTimestamp(inst.createdAt),
		Spec:      inst.spec,
	}, nil
}

// Platform implements the sandbox service: the sandbox runs on the host.
func (s *Service) Platform(ctx context.Context, r *api.PlatformRequest) (*api.PlatformResponse, error) {
	return &api.PlatformResponse{
		Platform: types.OCIPlatformToProto([]ocispec.Platform{platforms.DefaultSpec()})[0],
	}, nil
}

// StopSandbox releases the pod namespaces and files. Containers still running
// keep the namespaces they joined until they exit; CRI stops them first.
// Stopping twice is fine.
func (s *Service) StopSandbox(ctx context.Context, r *api.StopSandboxRequest) (*api.StopSandboxResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, err := s.findSandbox(r.GetSandboxID())
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	if err := s.stop(ctx, inst); err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	return &api.StopSandboxResponse{}, nil
}

// stop releases what the sandbox holds and marks it stopped. The caller holds
// s.mu.
func (s *Service) stop(ctx context.Context, inst *instance) error {
	if inst.stopped {
		return nil
	}
	if err := Cleanup(ctx, inst.bundle); err != nil {
		return fmt.Errorf("failed to clean up sandbox %q: %w", inst.cfg.ID, err)
	}
	inst.stopped = true
	inst.exitedAt = time.Now()
	close(inst.done)
	return nil
}

// WaitSandbox blocks until the sandbox is stopped.
func (s *Service) WaitSandbox(ctx context.Context, r *api.WaitSandboxRequest) (*api.WaitSandboxResponse, error) {
	s.mu.Lock()
	inst, err := s.findSandbox(r.GetSandboxID())
	s.mu.Unlock()
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	select {
	case <-inst.done:
	case <-ctx.Done():
		return nil, errgrpc.ToGRPC(ctx.Err())
	}
	return &api.WaitSandboxResponse{
		ExitStatus: 0,
		ExitedAt:   protobuf.ToTimestamp(inst.exitedAt),
	}, nil
}

// SandboxStatus reports the lifecycle state. The verbose info carries only
// shim specific keys; the CRI server builds its own verbose document.
func (s *Service) SandboxStatus(ctx context.Context, r *api.SandboxStatusRequest) (*api.SandboxStatusResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, err := s.findSandbox(r.GetSandboxID())
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	resp := &api.SandboxStatusResponse{
		SandboxID: inst.cfg.ID,
		Pid:       0,
		State:     runtime.PodSandboxState_SANDBOX_READY.String(),
		CreatedAt: protobuf.ToTimestamp(inst.createdAt),
	}
	if inst.stopped {
		resp.State = runtime.PodSandboxState_SANDBOX_NOTREADY.String()
		resp.ExitedAt = protobuf.ToTimestamp(inst.exitedAt)
	}
	if r.GetVerbose() {
		resp.Info = make(map[string]string, 4)
		for _, kv := range [][2]string{
			{BinaryName + "/netns", inst.cfg.NetNSPath},
			{BinaryName + "/ipcns", inst.pins.IPC},
			{BinaryName + "/utsns", inst.pins.UTS},
			{BinaryName + "/hostname", inst.cfg.Hostname},
		} {
			if kv[1] != "" {
				resp.Info[kv[0]] = kv[1]
			}
		}
	}
	return resp, nil
}

// PingSandbox implements the sandbox service.
func (s *Service) PingSandbox(ctx context.Context, r *api.PingRequest) (*api.PingResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.findSandbox(r.GetSandboxID()); err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	return &api.PingResponse{}, nil
}

// ShutdownSandbox stops the sandbox if needed, forgets it and ends the shim.
// The containers of the pod are gone by then: CRI removes them before it
// removes the pod. Shutting down twice, or an unknown sandbox, is fine.
func (s *Service) ShutdownSandbox(ctx context.Context, r *api.ShutdownSandboxRequest) (*api.ShutdownSandboxResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inst := s.sandbox; inst != nil {
		if err := s.stop(ctx, inst); err != nil {
			return nil, errgrpc.ToGRPC(err)
		}
		s.sandbox = nil
	}
	s.shutdown.Shutdown()
	return &api.ShutdownSandboxResponse{}, nil
}

// SandboxMetrics reports the cgroup v2 stats of the pod cgroup, encoded the
// way CRI expects sandbox metrics (a *cgroup2/stats.Metrics). The sandbox has
// no process of its own, so the aggregate pod cgroup is the only meaningful
// source. CRI polls this every second; the cgroup files are read outside the
// lock.
func (s *Service) SandboxMetrics(ctx context.Context, r *api.SandboxMetricsRequest) (*api.SandboxMetricsResponse, error) {
	s.mu.Lock()
	inst, err := s.findSandbox(r.GetSandboxID())
	s.mu.Unlock()
	if err != nil {
		return nil, errgrpc.ToGRPC(err)
	}
	id, cgroupParent := inst.cfg.ID, inst.cfg.CgroupParent
	if cgroupParent == "" {
		return nil, errgrpc.ToGRPCf(errdefs.ErrNotFound, "sandbox %q has no cgroup parent", id)
	}
	cg, err := cgroupsv2.Load(cgroupParent)
	if err != nil {
		return nil, errgrpc.ToGRPCf(err, "failed to load sandbox cgroup %q", cgroupParent)
	}
	stats, err := cg.StatFiltered(cgroupsv2.StatCPU | cgroupsv2.StatMemory)
	if err != nil {
		return nil, errgrpc.ToGRPCf(err, "failed to read sandbox cgroup %q", cgroupParent)
	}
	data, err := typeurl.MarshalAnyToProto(stats)
	if err != nil {
		return nil, errgrpc.ToGRPCf(err, "failed to marshal sandbox metrics")
	}
	return &api.SandboxMetricsResponse{Metrics: &types.Metric{
		Timestamp: protobuf.ToTimestamp(time.Now()),
		ID:        id,
		Data:      data,
	}}, nil
}

// UpdateSandbox is not implemented: the sandbox has no resources of its own.
func (s *Service) UpdateSandbox(ctx context.Context, r *api.UpdateSandboxRequest) (*api.UpdateSandboxResponse, error) {
	return nil, errgrpc.ToGRPCf(errdefs.ErrNotImplemented, "%s does not implement UpdateSandbox", BinaryName)
}
