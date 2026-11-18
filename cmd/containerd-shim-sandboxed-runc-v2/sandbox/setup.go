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
	"errors"
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
	"github.com/containerd/containerd/v2/contrib/seccomp"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/containerd/v2/pkg/sys/reaper"
)

// InitBinary is the PID 1 program of a pod that shares its PID namespace,
// built from cmd/containerd-sandbox-init and installed next to the shim.
const InitBinary = "containerd-sandbox-init"

const (
	// initPath is where the container program is mounted inside the
	// container: containerd-sandbox-init for a shared-PID pod, and the shim
	// binary, which never runs, for any other pod.
	initPath = "/" + InitBinary
	// runcDir is the bundle directory of the setup runc container, under the
	// sandbox bundle. The config.json of the sandbox bundle belongs to the
	// client that created the sandbox.
	runcDir = "runc"
	// runcRootfs is the empty rootfs of the container, relative to
	// runcDir.
	runcRootfs = "rootfs"
	// initUser is the user the container process runs as. It is neither
	// root nor nobody (65534), which workloads commonly run as.
	initUser = 65535
	// initOOMScoreAdj is what CRI gives a pause container.
	initOOMScoreAdj = -998
	// initStopTimeout bounds the wait for the container process to be
	// reaped after SIGKILL.
	initStopTimeout = 10 * time.Second
	// pinDir is the bundle directory holding the namespace pins, one file
	// per namespace type ("ipc", "uts", "pid").
	pinDir = "ns"
)

// Pins are the namespace files a sandbox bind-mounts to keep its namespaces
// alive without a process in them, as CRI does for the network namespace.
// An empty path means the pod does not have a namespace of its own
// of that type. The pinning follows nerdbox's shared resources implementation
// (github.com/containerd/nerdbox, internal/vminit/sharedresources).
type Pins struct {
	IPC string
	UTS string
	// PID is the PID namespace of a pod that shares it.
	PID string
}

// initProcess is the process of the setup runc container. The container id
// is the sandbox id, which "shim delete" force-deletes once the shim is gone.
// "runc create" creates the pod namespaces, sets the pod hostname, applies
// the pod sysctls and leaves the process waiting to exec its program, and the
// shim pins the namespaces from the process. For an ordinary pod the shim
// then kills the process and deletes the container, and only the pins
// remain. For a pod that shares its PID namespace the shim starts the
// container, and containerd-sandbox-init runs as PID 1 of the pod PID
// namespace for the life of the pod.
type initProcess struct {
	runtime *gorunc.Runc
	id      string
	pid     int
	// pidfd refers to the process even after it is reaped, when the kernel
	// may give its pid to another process. runc opens it while the process
	// is still its child, so it cannot refer to any other process.
	pidfd *os.File
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

// cgroupsPath returns the runc cgroups path of the container under the pod
// cgroup, as CRI computes it for the containers of the pod. A systemd slice
// becomes runc's "slice:prefix:name", and a cgroupfs path gets the container
// id appended.
func cgroupsPath(parent, id string) string {
	if base := path.Base(parent); strings.HasSuffix(base, ".slice") {
		return base + ":cri-containerd:" + id
	}
	return filepath.Join(parent, id)
}

// podNamespaces returns the namespaces the setup creates and the shim pins:
// the IPC and UTS namespaces the pod does not share with the host, and the
// PID namespace of a pod that shares one among its containers.
func podNamespaces(cfg *Config) []specs.LinuxNamespaceType {
	var ns []specs.LinuxNamespaceType
	if !cfg.HostIPC {
		ns = append(ns, specs.IPCNamespace)
	}
	if !cfg.HostNetwork {
		ns = append(ns, specs.UTSNamespace)
	}
	if cfg.SharedPID {
		ns = append(ns, specs.PIDNamespace)
	}
	return ns
}

// runcSpec is the OCI spec of the setup runc container. Besides the pod
// namespaces, hostname and sysctls, it confines the process as a pause
// container is: no capabilities, no new privileges, the masked and read-only
// /proc paths and /proc and /dev mounts of a default container spec, and the
// OOM score and CPU shares of a pause container. Only the container of a
// shared-PID pod, which the shim starts, gets containerd's default seccomp
// profile. The program of any other container never runs, and loading the
// profile roughly doubles the time the setup takes.
//
// TODO: the pod SELinux label is not applied yet, as for the files the shim
// creates (see the SELinux TODO in CreateSandbox).
func runcSpec(ctx context.Context, cfg *Config, binary string) (*specs.Spec, error) {
	defaults, err := oci.GenerateSpec(ctx, nil, &containers.Container{ID: cfg.ID})
	if err != nil {
		return nil, err
	}
	ns := []specs.LinuxNamespace{{Type: specs.MountNamespace}}
	if cfg.NetNSPath != "" {
		ns = append(ns, specs.LinuxNamespace{Type: specs.NetworkNamespace, Path: cfg.NetNSPath})
	}
	for _, t := range podNamespaces(cfg) {
		ns = append(ns, specs.LinuxNamespace{Type: t})
	}
	var mounts []specs.Mount
	for _, m := range defaults.Mounts {
		if m.Destination == "/proc" || m.Destination == "/dev" {
			mounts = append(mounts, m)
		}
	}
	mounts = append(mounts, specs.Mount{Destination: initPath, Type: "bind", Source: binary, Options: []string{"bind", "ro", "nosuid", "nodev"}})
	oomScoreAdj := initOOMScoreAdj
	// CRI gives a pause container the same CPU shares
	// (WithDefaultSandboxShares).
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
		Root:   &specs.Root{Path: runcRootfs, Readonly: true},
		Mounts: mounts,
		Linux: &specs.Linux{
			Resources:     &specs.LinuxResources{CPU: &specs.LinuxCPU{Shares: &shares}},
			Namespaces:    ns,
			Sysctl:        cfg.Sysctls,
			MaskedPaths:   defaults.Linux.MaskedPaths,
			ReadonlyPaths: defaults.Linux.ReadonlyPaths,
		},
	}
	if !cfg.HostNetwork {
		s.Hostname = cfg.Hostname
	}
	if cfg.CgroupParent != "" {
		s.Linux.CgroupsPath = cgroupsPath(cfg.CgroupParent, cfg.ID)
	}
	if cfg.SharedPID {
		// DefaultProfile derives the profile from the capabilities set above.
		s.Linux.Seccomp = seccomp.DefaultProfile(s)
	}
	return s, nil
}

// setupNamespaces creates the pod namespaces through the setup runc container
// in <bundle>/runc and pins them under <bundle>/ns (see initProcess). For an
// ordinary pod it deletes the container before it returns. For a shared-PID
// pod it returns the created container process, which the caller starts. It
// does not run runc for a pod that does not have a namespace of its own. On
// error it force-deletes the container, and the Cleanup of the caller removes
// the pins.
func setupNamespaces(ctx context.Context, bundle string, cfg *Config) (_ Pins, _ *initProcess, retErr error) {
	podNS := podNamespaces(cfg)
	if len(podNS) == 0 {
		return Pins{}, nil, nil
	}
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return Pins{}, nil, err
	}
	// runc create requires the program to exist, although only a shared-PID
	// pod runs it.
	binary, err := os.Executable()
	if cfg.SharedPID {
		binary, err = initBinary()
	}
	if err != nil {
		return Pins{}, nil, err
	}
	// The container uses the runc, root and cgroup driver of the containers
	// of the pod, from the runc options "shim start" persisted.
	opts, err := runc.ReadOptions(bundle)
	if err != nil {
		return Pins{}, nil, fmt.Errorf("failed to read the runc options: %w", err)
	}
	dir := filepath.Join(bundle, runcDir)
	rootfs := filepath.Join(dir, runcRootfs)
	if err := os.MkdirAll(rootfs, 0o711); err != nil {
		return Pins{}, nil, err
	}
	// The container user needs search permission on "/" to exec its program.
	// MkdirAll applies the umask of the shim, and Chmod does not.
	if err := os.Chmod(rootfs, 0o711); err != nil {
		return Pins{}, nil, err
	}
	spec, err := runcSpec(ctx, cfg, binary)
	if err != nil {
		return Pins{}, nil, err
	}
	config, err := json.Marshal(spec)
	if err != nil {
		return Pins{}, nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), config, 0o600); err != nil {
		return Pins{}, nil, fmt.Errorf("failed to write the container spec: %w", err)
	}

	p := &initProcess{
		runtime: process.NewRunc(opts.GetRoot(), dir, ns, opts.GetBinaryName(), opts.GetSystemdCgroup()),
		id:      cfg.ID,
		exited:  make(chan struct{}),
	}
	// The subscription comes before runc create so that the exit cannot be
	// missed. WaitPid learns the pid from the pid file runc create writes.
	ec := reaper.Default.Subscribe()
	pidCh := make(chan int, 1)
	go func() {
		if e, ok := reaper.Default.WaitPid(ec, pidCh, p.reaped); ok {
			p.exit = e
			close(p.exited)
		}
	}()
	defer func() {
		if retErr != nil {
			close(pidCh)
			// A failed create may have left runc state and a process. The
			// failure may be the cancellation of ctx itself.
			if err := p.delete(context.WithoutCancel(ctx)); err != nil {
				log.G(ctx).WithError(err).WithField("id", p.id).Warn("failed to delete the sandbox container after a failed setup")
			}
		}
	}()

	nullIO, err := gorunc.NewNullIO()
	if err != nil {
		return Pins{}, nil, err
	}
	defer nullIO.Close()
	// A path under the bundle can exceed the 108 bytes a unix socket path
	// allows, so the socket lives in a temporary directory.
	pidfdSocket, err := gorunc.NewTempPidfdSocket()
	if err != nil {
		return Pins{}, nil, fmt.Errorf("failed to create the pidfd socket: %w", err)
	}
	defer pidfdSocket.Close()
	pidFile := filepath.Join(dir, process.InitPidFile)
	if err := p.runtime.Create(ctx, p.id, dir, &gorunc.CreateOpts{
		IO:           nullIO,
		PidFile:      pidFile,
		PidfdSocket:  pidfdSocket,
		NoPivot:      opts.GetNoPivotRoot(),
		NoNewKeyring: opts.GetNoNewKeyring(),
	}); err != nil {
		// runc writes the reason for the failure to its log.
		if msg, lerr := process.LastRuntimeError(p.runtime); lerr == nil && msg != "" {
			err = fmt.Errorf("%s: %w", msg, err)
		}
		return Pins{}, nil, fmt.Errorf("failed to create the sandbox container: %w", err)
	}
	// runc sends the pidfd before runc create returns. Closing the socket
	// ends the wait for a runtime that never sends one.
	recvCtx, cancel := context.WithTimeout(ctx, initStopTimeout)
	defer cancel()
	stopRecv := context.AfterFunc(recvCtx, func() { pidfdSocket.Close() })
	p.pidfd, err = pidfdSocket.ReceivePidfd()
	stopRecv()
	if err != nil {
		return Pins{}, nil, fmt.Errorf("failed to receive the sandbox container pidfd: %w", err)
	}
	if p.pid, err = gorunc.ReadPidFile(pidFile); err != nil {
		return Pins{}, nil, fmt.Errorf("failed to read the sandbox container pid: %w", err)
	}
	pidCh <- p.pid

	// The pins keep the namespaces alive after the process exits. The
	// containers join the pins, whose paths stay valid after the kernel
	// gives the pid to another process.
	var pins Pins
	if err := os.MkdirAll(filepath.Join(bundle, pinDir), 0o700); err != nil {
		return Pins{}, nil, fmt.Errorf("failed to create namespace pin directory: %w", err)
	}
	for _, t := range podNS {
		target := filepath.Join(bundle, pinDir, string(t))
		if err := pin(fmt.Sprintf("/proc/%d/ns/%s", p.pid, t), target); err != nil {
			return Pins{}, nil, err
		}
		switch t {
		case specs.IPCNamespace:
			pins.IPC = target
		case specs.UTSNamespace:
			pins.UTS = target
		case specs.PIDNamespace:
			pins.PID = target
		}
	}
	// The pins were taken through /proc/<pid>, which names another process
	// only once the container process has been reaped.
	if p.reaped() {
		return Pins{}, nil, errors.New("the sandbox container process exited before its namespaces were pinned")
	}

	if cfg.SharedPID {
		return pins, p, nil
	}
	if _, err := p.stop(ctx); err != nil {
		return Pins{}, nil, err
	}
	return pins, nil, nil
}

// signal sends sig to the container process through its pidfd. The kernel
// fails it with ESRCH once the process has been reaped.
func (p *initProcess) signal(sig unix.Signal) error {
	rc, err := p.pidfd.SyscallConn()
	if err != nil {
		return err
	}
	// Control keeps the descriptor open while fn runs. A concurrent delete
	// cannot close it in between, and the kernel cannot give its number to
	// another file.
	if cerr := rc.Control(func(fd uintptr) {
		err = unix.PidfdSendSignal(int(fd), sig, nil, 0)
	}); cerr != nil {
		return cerr
	}
	return err
}

// reaped reports whether the container process has been reaped. From then on
// the kernel may give its pid to another process; a process that has exited
// but is not reaped yet still holds its pid. reaped reports true after delete
// closes the pidfd.
func (p *initProcess) reaped() bool {
	err := p.signal(0)
	return errors.Is(err, unix.ESRCH) || errors.Is(err, os.ErrClosed)
}

// stop kills the container process and deletes the container.
func (p *initProcess) stop(ctx context.Context) (gorunc.Exit, error) {
	exit, err := p.kill(ctx)
	if err != nil {
		return exit, err
	}
	return exit, p.delete(ctx)
}

// kill sends SIGKILL to the container process, unless it has exited, and
// waits up to initStopTimeout for the exit. The process of a shared-PID pod
// is containerd-sandbox-init, PID 1 of its namespace without signal handlers,
// and the kernel does not deliver any other signal from the shim to it.
func (p *initProcess) kill(ctx context.Context) (gorunc.Exit, error) {
	select {
	case <-p.exited:
	default:
		if err := p.signal(unix.SIGKILL); err != nil {
			// The process may have exited since the check above; the wait
			// below reports it.
			log.G(ctx).WithError(err).WithField("id", p.id).Debug("failed to kill the sandbox container")
		}
		select {
		case <-p.exited:
		case <-time.After(initStopTimeout):
			return gorunc.Exit{}, fmt.Errorf("the sandbox container process (pid %d) did not exit after SIGKILL", p.pid)
		case <-ctx.Done():
			return gorunc.Exit{}, ctx.Err()
		}
	}
	return p.exit, nil
}

// delete closes the pidfd and force-deletes the runc container. runc does not
// wait for a process that has already exited, and deleting a container that
// does not exist succeeds.
func (p *initProcess) delete(ctx context.Context) error {
	if p.pidfd != nil {
		p.pidfd.Close()
	}
	if err := p.runtime.Delete(ctx, p.id, &gorunc.DeleteOpts{Force: true}); err != nil {
		return fmt.Errorf("failed to delete the sandbox container: %w", err)
	}
	return nil
}

// pin bind-mounts the namespace file src onto a new empty file at target so
// that the namespace persists without a process in it.
func pin(src, target string) error {
	f, err := os.OpenFile(target, os.O_RDONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("failed to create namespace pin %s: %w", target, err)
	}
	f.Close()
	if err := unix.Mount(src, target, "none", unix.MS_BIND, ""); err != nil {
		os.Remove(target)
		return fmt.Errorf("failed to pin namespace %s at %s: %w", src, target, err)
	}
	return nil
}
