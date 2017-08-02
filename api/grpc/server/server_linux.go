package server

import (
	"fmt"

	"github.com/containerd/containerd/api/grpc/types"
	"github.com/containerd/containerd/specs"
	"github.com/containerd/containerd/supervisor"
	"github.com/opencontainers/runc/libcontainer/system"
	ocs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/net/context"
)

var clockTicksPerSecond = uint64(system.GetClockTicks())

func (s *apiServer) AddProcess(ctx context.Context, r *types.AddProcessRequest) (*types.AddProcessResponse, error) {
	process := &specs.ProcessSpec{
		Terminal: r.Terminal,
		Args:     r.Args,
		Env:      r.Env,
		Cwd:      r.Cwd,
	}
	process.User = ocs.User{
		UID:            r.User.Uid,
		GID:            r.User.Gid,
		AdditionalGids: r.User.AdditionalGids,
	}
	// for backwards compat in the API set eibp
	process.Capabilities = &ocs.LinuxCapabilities{
		Bounding:    r.Capabilities,
		Effective:   r.Capabilities,
		Inheritable: r.Capabilities,
		Permitted:   r.Capabilities,
	}
	process.ApparmorProfile = r.ApparmorProfile
	process.SelinuxLabel = r.SelinuxLabel
	process.NoNewPrivileges = r.NoNewPrivileges
	for _, rl := range r.Rlimits {
		process.Rlimits = append(process.Rlimits, ocs.POSIXRlimit{
			Type: rl.Type,
			Soft: rl.Soft,
			Hard: rl.Hard,
		})
	}
	if r.Id == "" {
		return nil, fmt.Errorf("container id cannot be empty")
	}
	if r.Pid == "" {
		return nil, fmt.Errorf("process id cannot be empty")
	}
	e := &supervisor.AddProcessTask{}
	e.WithContext(ctx)
	e.ID = r.Id
	e.PID = r.Pid
	e.ProcessSpec = process
	e.Stdin = r.Stdin
	e.Stdout = r.Stdout
	e.Stderr = r.Stderr
	e.StartResponse = make(chan supervisor.StartResponse, 1)
	s.sv.SendTask(e)
	if err := <-e.ErrorCh(); err != nil {
		return nil, err
	}
	sr := <-e.StartResponse
	return &types.AddProcessResponse{SystemPid: uint32(sr.ExecPid)}, nil
}
