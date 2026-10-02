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
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/moby/sys/mountinfo"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/internal/cri/sandboxfiles"
)

// Everything the sandbox creates lives under the shim bundle at fixed paths
// (see the package doc):
//
//	<bundle>/ns/{ipc,uts}   namespace pins
//	<bundle>/hostname       the pod hostname
//	<bundle>/hosts          a copy of the host /etc/hosts
//	<bundle>/resolv.conf    the pod DNS configuration
//	<bundle>/shm            the pod /dev/shm tmpfs

// setupFiles creates the pod shared files and returns the mounts that expose
// them to the containers of the pod; CRI picks the sources up from the
// sandbox spec.
func setupFiles(bundle string, cfg *Config) ([]specs.Mount, error) {
	var mounts []specs.Mount
	bind := func(destination, source string) {
		mounts = append(mounts, specs.Mount{
			Destination: destination,
			Type:        "bind",
			Source:      source,
			Options:     []string{"rbind"},
		})
	}

	hostname := filepath.Join(bundle, sandboxfiles.HostnameFile)
	if err := os.WriteFile(hostname, sandboxfiles.HostnameContent(cfg.Hostname), 0o644); err != nil {
		return nil, fmt.Errorf("failed to write %s: %w", hostname, err)
	}
	bind(sandboxfiles.EtcHostname, hostname)

	hosts := filepath.Join(bundle, sandboxfiles.HostsFile)
	if err := copyFile(sandboxfiles.EtcHosts, hosts); err != nil {
		return nil, fmt.Errorf("failed to create %s: %w", hosts, err)
	}
	bind(sandboxfiles.EtcHosts, hosts)

	resolvConf := filepath.Join(bundle, sandboxfiles.ResolvConfFile)
	if cfg.DNS != nil {
		content := sandboxfiles.ResolvConfContent(cfg.DNS.GetServers(), cfg.DNS.GetSearches(), cfg.DNS.GetOptions())
		if err := os.WriteFile(resolvConf, content, 0o644); err != nil {
			return nil, fmt.Errorf("failed to write %s: %w", resolvConf, err)
		}
	} else {
		// No DNS config means the host resolver configuration, as for pause.
		if err := copyFile(sandboxfiles.ResolvConfPath, resolvConf); err != nil {
			return nil, fmt.Errorf("failed to create %s: %w", resolvConf, err)
		}
	}
	bind(sandboxfiles.ResolvConfPath, resolvConf)

	if !cfg.HostIPC {
		shm := filepath.Join(bundle, sandboxfiles.ShmDir)
		if err := os.Mkdir(shm, 0o700); err != nil && !os.IsExist(err) {
			return nil, fmt.Errorf("failed to create %s: %w", shm, err)
		}
		if err := unix.Mount("shm", shm, "tmpfs", unix.MS_NOEXEC|unix.MS_NOSUID|unix.MS_NODEV, sandboxfiles.ShmMountData(cfg.ShmSize)); err != nil {
			return nil, fmt.Errorf("failed to mount the pod shm at %s: %w", shm, err)
		}
		bind(sandboxfiles.DevShm, shm)
	}
	return mounts, nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// Cleanup removes everything the sandbox shim created under bundle: it
// detaches the mounts at their fixed paths (the namespace pins and the shm
// tmpfs) and removes the pin and shared files. It is idempotent and relies on
// no state, so it serves create failure, StopSandbox and "shim delete" alike;
// a mount left behind would keep containerd from deleting the bundle.
func Cleanup(ctx context.Context, bundle string) error {
	var errs []error
	for _, p := range []string{
		filepath.Join(bundle, pinDir, ipcPin),
		filepath.Join(bundle, pinDir, utsPin),
		filepath.Join(bundle, sandboxfiles.ShmDir),
	} {
		// Only a mount point is unmounted: umount(2) on a plain file needs
		// the same privilege as on a mount, and answers EPERM before EINVAL.
		mounted, err := mountinfo.Mounted(p)
		if err != nil {
			if !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("failed to check %s: %w", p, err))
			}
			continue
		}
		if !mounted {
			continue
		}
		if err := mount.UnmountAll(p, unix.MNT_DETACH); err != nil {
			errs = append(errs, fmt.Errorf("failed to detach %s: %w", p, err))
		}
	}
	if len(errs) > 0 {
		// Nothing is removed while one of the mounts is still in place.
		return errors.Join(errs...)
	}
	for _, name := range []string{pinDir, sandboxfiles.ShmDir, sandboxfiles.HostnameFile, sandboxfiles.HostsFile, sandboxfiles.ResolvConfFile} {
		if err := os.RemoveAll(filepath.Join(bundle, name)); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}
