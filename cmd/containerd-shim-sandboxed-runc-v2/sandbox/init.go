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
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	gorunc "github.com/containerd/go-runc"
	"github.com/containerd/log"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/process"
	"github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/runc"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/sys/reaper"
)

// InitBinary is the PID 1 program of a pod that shares its PID namespace,
// built from cmd/containerd-sandbox-init and installed next to the shim.
const InitBinary = "containerd-sandbox-init"

const (
	// initPath is where the program is mounted inside its container.
	initPath = "/" + InitBinary
	// initRootfs is the rootfs of the init container, relative to the
	// sandbox bundle: an empty tmpfs.
	initRootfs = "rootfs"
	// pidPin is the pin of the PID namespace the init is PID 1 of, beside
	// the IPC and UTS pins.
	pidPin = "pid"
	// initUser is the user the init runs as: not root, and not nobody
	// (65534), which workloads commonly run as.
	initUser = 65535
	// initOOMScoreAdj is what CRI gives a pause container.
	initOOMScoreAdj = -998
	// initStopTimeout bounds the wait for the init to be reaped after
	// SIGKILL.
	initStopTimeout = 10 * time.Second
)

// initProcess is the containerd-sandbox-init runc container of a pod that
// shares its PID namespace (see the package doc). Its container id is the
// sandbox id, which is what "shim delete" force-deletes once the shim is
// gone.
type initProcess struct {
	runtime *gorunc.Runc
	id      string
	pid     int
	// pidNS is the pin of the PID namespace, the path the containers join.
	pidNS string
	// exited is closed once the exit of the process has been reaped; exit
	// is valid from then on.
	exited chan struct{}
	exit   gorunc.Exit
}

// initBinary locates containerd-sandbox-init: next to the shim binary, then
// in PATH.
func initBinary() (string, error) {
	if self, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(self), InitBinary)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	p, err := exec.LookPath(InitBinary)
	if err != nil {
		return "", fmt.Errorf("%s is not installed next to %s or in PATH: %w", InitBinary, BinaryName, errdefs.ErrFailedPrecondition)
	}
	return p, nil
}

// cgroupsPath places the init container under the pod cgroup the way CRI
// places the containers of the pod: a systemd slice becomes runc's
// "slice:prefix:name", a cgroupfs path gets the container id appended.
func cgroupsPath(parent, id string) string {
	if base := path.Base(parent); strings.HasSuffix(base, ".slice") {
		return base + ":cri-containerd:" + id
	}
	return filepath.Join(parent, id)
}

// initSpec is the OCI spec of the init container. The rootfs holds nothing
// but the program; the process has no capabilities, cannot gain privileges
// and is scheduled and OOM-scored as a pause container is. It joins the pod
// namespaces the sandbox holds and creates the PID namespace. The hostname
// is left alone: it is that of the joined UTS namespace.
//
// TODO: seccomp, AppArmor and SELinux are not applied yet, as for the files
// the shim creates (see the SELinux TODO in CreateSandbox).
func initSpec(cfg *Config, pins Pins, binary string) *specs.Spec {
	oomScoreAdj := initOOMScoreAdj
	// The CPU shares of a pause container (WithDefaultSandboxShares).
	shares := uint64(2)
	s := &specs.Spec{
		Version: specs.Version,
		Process: &specs.Process{
			User:            specs.User{UID: initUser, GID: initUser},
			Args:            []string{initPath},
			Cwd:             "/",
			Capabilities:    &specs.LinuxCapabilities{},
			NoNewPrivileges: true,
			OOMScoreAdj:     &oomScoreAdj,
		},
		Root: &specs.Root{Path: initRootfs, Readonly: true},
		Mounts: []specs.Mount{
			{Destination: "/proc", Type: "proc", Source: "proc", Options: []string{"nosuid", "noexec", "nodev"}},
			{Destination: "/dev", Type: "tmpfs", Source: "tmpfs", Options: []string{"nosuid", "strictatime", "mode=755", "size=65536k"}},
			{Destination: initPath, Type: "bind", Source: binary, Options: []string{"bind", "ro", "nosuid", "nodev"}},
		},
		Linux: &specs.Linux{
			Resources:  &specs.LinuxResources{CPU: &specs.LinuxCPU{Shares: &shares}},
			Namespaces: append([]specs.LinuxNamespace{{Type: specs.PIDNamespace}, {Type: specs.MountNamespace}}, podNamespaces(cfg, pins)...),
		},
	}
	if cfg.CgroupParent != "" {
		s.Linux.CgroupsPath = cgroupsPath(cfg.CgroupParent, cfg.ID)
	}
	return s
}

// createInit runs "runc create" for the init container in the sandbox
// bundle: the rootfs is a tmpfs mounted at <bundle>/rootfs, so that the
// mount points runc creates in it leave nothing behind, and the spec is
// written as <bundle>/config.json. From here on the process exists and is
// PID 1 of the new PID namespace, pinned at <bundle>/ns/pid, waiting in runc
// for StartSandbox to exec the program. On error nothing is left running;
// the Cleanup the caller runs detaches the rootfs and the pin.
func createInit(ctx context.Context, bundle string, cfg *Config, pins Pins) (_ *initProcess, retErr error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	binary, err := initBinary()
	if err != nil {
		return nil, err
	}
	// The runc options of the runtime handler, persisted by "shim start":
	// the same runc, root and cgroup driver as the containers of the pod.
	opts, err := runc.ReadOptions(bundle)
	if err != nil {
		return nil, fmt.Errorf("failed to read the runc options: %w", err)
	}
	rootfs := filepath.Join(bundle, initRootfs)
	if err := unix.Mount("tmpfs", rootfs, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=0755,size=1m"); err != nil {
		return nil, fmt.Errorf("failed to mount the init rootfs at %s: %w", rootfs, err)
	}
	config, err := json.Marshal(initSpec(cfg, pins, binary))
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(bundle, "config.json"), config, 0o600); err != nil {
		return nil, fmt.Errorf("failed to write the init spec: %w", err)
	}

	p := &initProcess{
		runtime: process.NewRunc(opts.GetRoot(), bundle, ns, opts.GetBinaryName(), opts.GetSystemdCgroup()),
		id:      cfg.ID,
		exited:  make(chan struct{}),
	}
	// Subscribed before the process exists so that its exit cannot be
	// missed; the watcher learns the pid once runc reports it.
	ec := reaper.Default.Subscribe()
	pidCh := make(chan int, 1)
	go p.watch(ec, pidCh)
	defer func() {
		if retErr != nil {
			close(pidCh)
			// A failed create may have left runc state and a process.
			if err := p.runtime.Delete(ctx, p.id, &gorunc.DeleteOpts{Force: true}); err != nil {
				log.G(ctx).WithError(err).WithField("id", p.id).Debugf("failed to delete %s after a failed create", InitBinary)
			}
		}
	}()

	nullIO, err := gorunc.NewNullIO()
	if err != nil {
		return nil, err
	}
	defer nullIO.Close()
	pidFile := filepath.Join(bundle, process.InitPidFile)
	if err := p.runtime.Create(ctx, p.id, bundle, &gorunc.CreateOpts{
		IO:           nullIO,
		PidFile:      pidFile,
		NoPivot:      opts.GetNoPivotRoot(),
		NoNewKeyring: opts.GetNoNewKeyring(),
	}); err != nil {
		return nil, fmt.Errorf("failed to create the %s container: %w", InitBinary, err)
	}
	if p.pid, err = gorunc.ReadPidFile(pidFile); err != nil {
		return nil, fmt.Errorf("failed to read the %s pid: %w", InitBinary, err)
	}
	pidCh <- p.pid
	// Pinned like the IPC and UTS namespaces, so that the path the
	// containers join never names whatever process reuses the pid.
	if err := os.MkdirAll(filepath.Join(bundle, pinDir), 0o700); err != nil {
		return nil, fmt.Errorf("failed to create namespace pin directory: %w", err)
	}
	p.pidNS = filepath.Join(bundle, pinDir, pidPin)
	if err := pin(fmt.Sprintf("/proc/%d/ns/pid", p.pid), p.pidNS); err != nil {
		return nil, err
	}
	return p, nil
}

// watch delivers the exit of the init through exited. It drains the reaper
// subscription throughout (a subscriber that falls behind slows every reap
// in the shim) and remembers the exits seen before the pid is known, since
// the process can be reaped between "runc create" and the pid file read.
func (p *initProcess) watch(ec chan gorunc.Exit, pidCh <-chan int) {
	defer reaper.Default.Unsubscribe(ec)
	pid := -1
	seen := map[int]gorunc.Exit{}
	for {
		select {
		case id, ok := <-pidCh:
			if !ok {
				return
			}
			if e, ok := seen[id]; ok {
				p.finish(e)
				return
			}
			pid, seen = id, nil
		case e, ok := <-ec:
			if !ok {
				return
			}
			switch {
			case pid < 0:
				seen[e.Pid] = e
			case e.Pid == pid:
				p.finish(e)
				return
			}
		}
	}
}

func (p *initProcess) finish(e gorunc.Exit) {
	p.exit = e
	close(p.exited)
}

// stop ends the init with SIGKILL, the one signal it does not ignore, waits
// for its exit to be reaped and deletes its runc container. A process that
// already exited on its own is only deleted; the exit is the same either
// way. The wait is bounded by initStopTimeout.
func (p *initProcess) stop(ctx context.Context) (gorunc.Exit, error) {
	select {
	case <-p.exited:
	default:
		if err := p.runtime.Kill(ctx, p.id, int(unix.SIGKILL), nil); err != nil {
			// The process may have exited in the meantime; the wait tells.
			log.G(ctx).WithError(err).WithField("id", p.id).Debugf("failed to kill %s", InitBinary)
		}
		select {
		case <-p.exited:
		case <-time.After(initStopTimeout):
			return gorunc.Exit{}, fmt.Errorf("%s (pid %d) did not exit after SIGKILL", InitBinary, p.pid)
		case <-ctx.Done():
			return gorunc.Exit{}, ctx.Err()
		}
	}
	if err := p.runtime.Delete(ctx, p.id, &gorunc.DeleteOpts{Force: true}); err != nil {
		return p.exit, fmt.Errorf("failed to delete the %s container: %w", InitBinary, err)
	}
	return p.exit, nil
}
