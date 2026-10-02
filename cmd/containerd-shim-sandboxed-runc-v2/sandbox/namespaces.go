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
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"golang.org/x/sys/unix"
)

// Pod namespaces without a process; see the package doc. The approach follows
// nerdbox's shared resources implementation (github.com/containerd/nerdbox,
// internal/vminit/sharedresources).

const (
	// pinDir is the bundle directory holding the namespace pins.
	pinDir = "ns"
	ipcPin = "ipc"
	utsPin = "uts"
)

// Pins are the bind-mounted namespace files a sandbox holds. An empty path
// means the pod uses the host namespace of that type.
type Pins struct {
	IPC string
	UTS string
}

// setupNamespaces creates and pins the pod namespaces under the bundle and
// applies the hostname and sysctls to them. On error the caller runs Cleanup.
func setupNamespaces(bundle string, cfg *Config) (Pins, error) {
	ipc, uts := !cfg.HostIPC, !cfg.HostNetwork
	if !ipc && !uts {
		// Nothing to hold, and the sysctl policy leaves nothing to apply to
		// namespaces the pod shares with the host.
		return Pins{}, nil
	}
	dir := filepath.Join(bundle, pinDir)
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return Pins{}, fmt.Errorf("failed to create namespace pin directory: %w", err)
	}

	var pins Pins
	errCh := make(chan error, 1)
	go func() {
		// The thread is locked while it is in the pod namespaces and put
		// back into the host namespaces before it is released, whether the
		// work succeeded or not: a thread Go cannot discard (the main thread)
		// must never stay in the pod. The pins keep the namespaces alive.
		runtime.LockOSThread()
		host, err := openThreadNamespaces()
		if err != nil {
			errCh <- err
			return
		}
		err = func() error {
			if cfg.NetNSPath != "" {
				// Entered first so that net sysctls apply to the pod network
				// namespace.
				if err := setns(cfg.NetNSPath, unix.CLONE_NEWNET); err != nil {
					return err
				}
			}
			var flags int
			if ipc {
				flags |= unix.CLONE_NEWIPC
			}
			if uts {
				flags |= unix.CLONE_NEWUTS
			}
			if err := unix.Unshare(flags); err != nil {
				return fmt.Errorf("failed to create pod namespaces: %w", err)
			}
			if uts {
				if err := unix.Sethostname([]byte(cfg.Hostname)); err != nil {
					return fmt.Errorf("failed to set hostname %q: %w", cfg.Hostname, err)
				}
			}
			for _, k := range slices.Sorted(maps.Keys(cfg.Sysctls)) {
				if err := writeSysctl(k, cfg.Sysctls[k]); err != nil {
					return err
				}
			}
			tid := unix.Gettid()
			if ipc {
				p := filepath.Join(dir, ipcPin)
				if err := pin(threadNamespacePath(tid, "ipc"), p); err != nil {
					return err
				}
				pins.IPC = p
			}
			if uts {
				p := filepath.Join(dir, utsPin)
				if err := pin(threadNamespacePath(tid, "uts"), p); err != nil {
					return err
				}
				pins.UTS = p
			}
			return nil
		}()
		if rerr := host.restore(); rerr != nil {
			// The thread stays locked and is discarded with the goroutine.
			errCh <- errors.Join(err, rerr)
			return
		}
		runtime.UnlockOSThread()
		errCh <- err
	}()
	if err := <-errCh; err != nil {
		return Pins{}, err
	}
	return pins, nil
}

// threadNamespaces holds the namespace files of a thread before it moves,
// so that it can be moved back.
type threadNamespaces struct {
	net, ipc, uts int
}

func openThreadNamespaces() (*threadNamespaces, error) {
	t := &threadNamespaces{net: -1, ipc: -1, uts: -1}
	for _, ns := range []struct {
		name string
		fd   *int
	}{{"net", &t.net}, {"ipc", &t.ipc}, {"uts", &t.uts}} {
		fd, err := unix.Open("/proc/thread-self/ns/"+ns.name, unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			t.close()
			return nil, fmt.Errorf("failed to open the %s namespace of the thread: %w", ns.name, err)
		}
		*ns.fd = fd
	}
	return t, nil
}

// restore moves the thread back into its original namespaces and closes them.
func (t *threadNamespaces) restore() error {
	defer t.close()
	var errs []error
	for _, ns := range []struct {
		fd    int
		flag  int
		which string
	}{{t.uts, unix.CLONE_NEWUTS, "uts"}, {t.ipc, unix.CLONE_NEWIPC, "ipc"}, {t.net, unix.CLONE_NEWNET, "net"}} {
		if err := unix.Setns(ns.fd, ns.flag); err != nil {
			errs = append(errs, fmt.Errorf("failed to move the thread back into the host %s namespace: %w", ns.which, err))
		}
	}
	return errors.Join(errs...)
}

func (t *threadNamespaces) close() {
	for _, fd := range []int{t.net, t.ipc, t.uts} {
		if fd >= 0 {
			unix.Close(fd)
		}
	}
	t.net, t.ipc, t.uts = -1, -1, -1
}

// setns moves the calling thread into the namespace pinned at path.
func setns(path string, nstype int) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("failed to open namespace %s: %w", path, err)
	}
	defer unix.Close(fd)
	if err := unix.Setns(fd, nstype); err != nil {
		return fmt.Errorf("failed to enter namespace %s: %w", path, err)
	}
	return nil
}

// threadNamespacePath is the namespace file of one thread. /proc/self/ns
// would name the namespaces of the main thread, not of the thread that
// changed them.
func threadNamespacePath(tid int, nsType string) string {
	return fmt.Sprintf("/proc/%d/task/%d/ns/%s", os.Getpid(), tid, nsType)
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

// writeSysctl sets a sysctl for the namespaces of the calling thread: the
// files under /proc/sys/net resolve against the network namespace of the
// caller and the IPC ones against its IPC namespace, so this must run on the
// thread that entered the pod namespaces.
func writeSysctl(key, value string) error {
	p, err := sysctlPath(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(value), 0o644); err != nil {
		return fmt.Errorf("failed to set sysctl %s=%q: %w", key, value, err)
	}
	return nil
}
