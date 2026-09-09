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

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"syscall"

	"github.com/containerd/cgroups/v3"
	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2"
	bootapi "github.com/containerd/containerd/api/runtime/bootstrap/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/api/types/runc/options"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/runtime-spec/specs-go/features"

	legacymanager "github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/manager"
	"github.com/containerd/containerd/v2/cmd/containerd-shim-sandboxed-runc-v2/sandbox"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/schedcore"
	"github.com/containerd/containerd/v2/pkg/shim"
	"github.com/containerd/containerd/v2/plugins"
)

// manager is the shim.Shim of the sandbox shim: what containerd runs as
// "start", "delete" and "-info". It starts one shim process per sandbox and
// never groups by OCI annotations, since the sandbox bundle has no config.json.
//
// TODO: consolidate with containerd-shim-runc-v2. newCommand, shimSocket and
// newShimSocket below are copies of the unexported helpers of its manager;
// export them (from pkg/shim or the manager package) and call them from both.
type manager struct {
	// legacy is the containerd-shim-runc-v2 manager. It serves the runtime
	// info (the same runtime type, the same runc, the same features) and the
	// cleanup of the task bundles joined to this shim.
	legacy shim.Shim
}

func newManager() shim.Shim {
	return &manager{legacy: legacymanager.NewShimManager(plugins.RuntimeRuncV2)}
}

// Name is the runtime type served: containerd reaches this binary through
// runtime_path, not through a runtime type of its own.
func (m *manager) Name() string {
	return m.legacy.Name()
}

func newCommand(ctx context.Context, id, containerdAddress string, debug bool) (*exec.Cmd, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	args := []string{
		"-namespace", ns,
		"-id", id,
		"-address", containerdAddress,
	}
	if debug {
		args = append(args, "-debug")
	}
	cmd := exec.Command(self, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GOMAXPROCS=4")
	cmd.Env = append(cmd.Env, "OTEL_SERVICE_NAME=containerd-shim-"+id)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	return cmd, nil
}

type shimSocket struct {
	addr string
	s    *net.UnixListener
	f    *os.File
}

func (s *shimSocket) Close() {
	if s.s != nil {
		s.s.Close()
	}
	if s.f != nil {
		s.f.Close()
	}
	_ = shim.RemoveSocket(s.addr)
}

func newShimSocket(ctx context.Context, root, path, id string, debug bool) (*shimSocket, error) {
	address, err := shim.CreateSocketAddress(ctx, root, path, id, debug)
	if err != nil {
		return nil, err
	}
	socket, err := shim.NewSocket(address)
	if err != nil {
		// A socket in use that accepts connections belongs to a shim that is
		// already running for this sandbox; a stale one is replaced.
		if !shim.SocketEaddrinuse(err) {
			return nil, fmt.Errorf("create new shim socket: %w", err)
		}
		if !debug && shim.CanConnect(address) {
			return &shimSocket{addr: address}, errdefs.ErrAlreadyExists
		}
		if err := shim.RemoveSocket(address); err != nil {
			return nil, fmt.Errorf("remove pre-existing socket: %w", err)
		}
		if socket, err = shim.NewSocket(address); err != nil {
			return nil, fmt.Errorf("try create new shim socket 2x: %w", err)
		}
	}
	s := &shimSocket{
		addr: address,
		s:    socket,
	}
	f, err := socket.File()
	if err != nil {
		s.Close()
		return nil, err
	}
	s.f = f
	return s, nil
}

// Start starts the shim process for a sandbox bundle. containerd calls it once
// per sandbox; the containers of the pod join the running shim through the
// bootstrap protocol without invoking "start" again.
func (m *manager) Start(ctx context.Context, opts *bootapi.BootstrapParams) (_ *bootapi.BootstrapResult, retErr error) {
	if cgroups.Mode() != cgroups.Unified {
		return nil, fmt.Errorf("%s requires cgroup v2 (the unified hierarchy) and this host runs cgroup v1: %w", sandbox.BinaryName, errdefs.ErrNotImplemented)
	}

	var params bootapi.BootstrapResult
	params.Version = 3
	params.Protocol = "ttrpc"

	id := opts.GetInstanceID()
	debugLog := opts.GetLogLevel() <= bootapi.LogLevel_LOG_LEVEL_DEBUG

	cmd, err := newCommand(ctx, id, opts.GetContainerdGrpcAddress(), debugLog)
	if err != nil {
		return nil, err
	}

	var sockets []*shimSocket
	defer func() {
		if retErr != nil {
			for _, s := range sockets {
				s.Close()
			}
		}
	}()

	socketDir := opts.GetSocketDir()
	if socketDir == "" {
		socketDir = filepath.Join(defaults.DefaultStateDir, "s")
	}
	// The socket is keyed by the sandbox id alone.
	s, err := newShimSocket(ctx, socketDir, opts.GetContainerdGrpcAddress(), id, false)
	if err != nil {
		if errdefs.IsAlreadyExists(err) {
			params.Address = s.addr
			return &params, nil
		}
		return nil, err
	}
	sockets = append(sockets, s)
	cmd.ExtraFiles = append(cmd.ExtraFiles, s.f)

	if debugLog {
		s, err = newShimSocket(ctx, socketDir, opts.GetContainerdGrpcAddress(), id, true)
		if err != nil {
			return nil, err
		}
		sockets = append(sockets, s)
		cmd.ExtraFiles = append(cmd.ExtraFiles, s.f)
	}

	// The shim is spawned from a locked thread so that a core scheduling
	// cookie created here is inherited by it; the thread is released on
	// every path.
	goruntime.LockOSThread()
	err = func() error {
		if os.Getenv("SCHED_CORE") != "" {
			if err := schedcore.Create(schedcore.ProcessGroup); err != nil {
				return fmt.Errorf("enable sched core support: %w", err)
			}
		}
		return cmd.Start()
	}()
	goruntime.UnlockOSThread()
	if err != nil {
		return nil, err
	}

	defer func() {
		if retErr != nil {
			cmd.Process.Kill()
		}
	}()
	// make sure to wait after start
	go cmd.Wait()

	var runcOpts options.Options
	if found, err := opts.FindExtension(&runcOpts); err != nil {
		return nil, fmt.Errorf("failed to fetch runc options: %w", err)
	} else if found {
		if shimCgroup := runcOpts.GetShimCgroup(); shimCgroup != "" {
			cg, err := cgroupsv2.Load(shimCgroup)
			if err != nil {
				return nil, fmt.Errorf("failed to load cgroup %s: %w", shimCgroup, err)
			}
			if err := cg.AddProc(uint64(cmd.Process.Pid)); err != nil {
				return nil, fmt.Errorf("failed to join cgroup %s: %w", shimCgroup, err)
			}
		}
	}

	if err := shim.AdjustOOMScore(cmd.Process.Pid); err != nil {
		return nil, fmt.Errorf("failed to adjust OOM score for shim: %w", err)
	}

	params.Address = sockets[0].addr
	return &params, nil
}

// Stop is "shim delete". containerd runs it in a bundle once the shim that
// served the bundle is gone: in the sandbox bundle, and in the bundle of every
// container task that joined the shim. Nothing needs telling apart: the runc
// task cleanup of containerd-shim-runc-v2 (force-delete the runc container of
// that id, detach its rootfs) does nothing in the sandbox bundle, and the
// sandbox cleanup (detach every mount under the bundle, remove the pod files)
// does nothing in a task bundle, so both run every time.
func (m *manager) Stop(ctx context.Context, id string) (shim.StopStatus, error) {
	status, err := m.legacy.Stop(ctx, id)
	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		return status, errors.Join(err, cwdErr)
	}
	return status, errors.Join(err, sandbox.Cleanup(ctx, filepath.Join(filepath.Dir(cwd), id)))
}

// Info is the runtime info of containerd-shim-runc-v2, the same runtime type
// and the features of the same runc, minus the user namespace: CRI advertises
// user namespace support for a handler from that feature, and this shim
// rejects user namespaced pods for now.
func (m *manager) Info(ctx context.Context, optionsR io.Reader) (*types.RuntimeInfo, error) {
	info, err := m.legacy.Info(ctx, optionsR)
	if err != nil || info.GetFeatures() == nil {
		return info, err
	}
	var feat features.Features
	if err := typeurl.UnmarshalTo(info.Features, &feat); err != nil {
		return nil, fmt.Errorf("failed to decode the runc features: %w", err)
	}
	if feat.Linux != nil {
		feat.Linux.Namespaces = slices.DeleteFunc(feat.Linux.Namespaces, func(ns string) bool { return ns == "user" })
	}
	if info.Features, err = typeurl.MarshalAnyToProto(&feat); err != nil {
		return nil, fmt.Errorf("failed to encode the runc features: %w", err)
	}
	return info, nil
}
