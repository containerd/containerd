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

package sbserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/cgroups/v3"
	"github.com/moby/sys/mountinfo"
	"github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/pkg/apparmor"
	"github.com/containerd/containerd/pkg/seccomp"
	"github.com/containerd/containerd/pkg/seutil"
)

const (
	// devShm is the default path of /dev/shm.
	devShm = "/dev/shm"
	// etcHosts is the default path of /etc/hosts file.
	etcHosts = "/etc/hosts"
	// etcHostname is the default path of /etc/hostname file.
	etcHostname = "/etc/hostname"
	// resolvConfPath is the abs path of resolv.conf on host or container.
	resolvConfPath = "/etc/resolv.conf"
)

// getSandboxRootDir returns the root directory for managing sandbox files,
// e.g. hosts files.
func (c *criService) getSandboxRootDir(id string) string {
	return filepath.Join(c.config.RootDir, sandboxesDir, id)
}

// getVolatileSandboxRootDir returns the root directory for managing volatile sandbox files,
// e.g. named pipes.
func (c *criService) getVolatileSandboxRootDir(id string) string {
	return filepath.Join(c.config.StateDir, sandboxesDir, id)
}

// getSandboxHostname returns the hostname file path inside the sandbox root directory.
func (c *criService) getSandboxHostname(id string) string {
	return filepath.Join(c.getSandboxRootDir(id), "hostname")
}

// getSandboxHosts returns the hosts file path inside the sandbox root directory.
func (c *criService) getSandboxHosts(id string) string {
	return filepath.Join(c.getSandboxRootDir(id), "hosts")
}

// getResolvPath returns resolv.conf filepath for specified sandbox.
func (c *criService) getResolvPath(id string) string {
	return filepath.Join(c.getSandboxRootDir(id), "resolv.conf")
}

// getSandboxDevShm returns the shm file path inside the sandbox root directory.
func (c *criService) getSandboxDevShm(id string) string {
	return filepath.Join(c.getVolatileSandboxRootDir(id), "shm")
}

// apparmorEnabled returns true if apparmor is enabled, supported by the host,
// if apparmor_parser is installed, and if we are not running docker-in-docker.
func (c *criService) apparmorEnabled() bool {
	if c.config.DisableApparmor {
		return false
	}
	return apparmor.HostSupports()
}

func (c *criService) seccompEnabled() bool {
	return seccomp.IsEnabled()
}

// openLogFile opens/creates a container log file.
func openLogFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
}

// unmountRecursive unmounts the target and all mounts underneath, starting with
// the deepest mount first.
func unmountRecursive(ctx context.Context, target string) error {
	toUnmount, err := mountinfo.GetMounts(mountinfo.PrefixFilter(target))
	if err != nil {
		return err
	}

	// Make the deepest mount be first
	sort.Slice(toUnmount, func(i, j int) bool {
		return len(toUnmount[i].Mountpoint) > len(toUnmount[j].Mountpoint)
	})

	for i, m := range toUnmount {
		if err := mount.UnmountAll(m.Mountpoint, unix.MNT_DETACH); err != nil {
			if i == len(toUnmount)-1 { // last mount
				return err
			}
			// This is some submount, we can ignore this error for now, the final unmount will fail if this is a real problem
			log.G(ctx).WithError(err).Debugf("failed to unmount submount %s", m.Mountpoint)
		}
	}
	return nil
}

// ensureRemoveAll wraps `os.RemoveAll` to check for specific errors that can
// often be remedied.
// Only use `ensureRemoveAll` if you really want to make every effort to remove
// a directory.
//
// Because of the way `os.Remove` (and by extension `os.RemoveAll`) works, there
// can be a race between reading directory entries and then actually attempting
// to remove everything in the directory.
// These types of errors do not need to be returned since it's ok for the dir to
// be gone we can just retry the remove operation.
//
// This should not return a `os.ErrNotExist` kind of error under any circumstances
func ensureRemoveAll(ctx context.Context, dir string) error {
	notExistErr := make(map[string]bool)

	// track retries
	exitOnErr := make(map[string]int)
	maxRetry := 50

	// Attempt to unmount anything beneath this dir first.
	if err := unmountRecursive(ctx, dir); err != nil {
		log.G(ctx).WithError(err).Debugf("failed to do initial unmount of %s", dir)
	}

	for {
		err := os.RemoveAll(dir)
		if err == nil {
			return nil
		}

		pe, ok := err.(*os.PathError)
		if !ok {
			return err
		}

		if os.IsNotExist(err) {
			if notExistErr[pe.Path] {
				return err
			}
			notExistErr[pe.Path] = true

			// There is a race where some subdir can be removed but after the
			// parent dir entries have been read.
			// So the path could be from `os.Remove(subdir)`
			// If the reported non-existent path is not the passed in `dir` we
			// should just retry, but otherwise return with no error.
			if pe.Path == dir {
				return nil
			}
			continue
		}

		if pe.Err != syscall.EBUSY {
			return err
		}
		if e := mount.Unmount(pe.Path, unix.MNT_DETACH); e != nil {
			return fmt.Errorf("error while removing %s: %w", dir, e)
		}

		if exitOnErr[pe.Path] == maxRetry {
			return err
		}
		exitOnErr[pe.Path]++
		time.Sleep(100 * time.Millisecond)
	}
}

var vmbasedRuntimes = []string{
	"io.containerd.kata",
}

func isVMBasedRuntime(runtimeType string) bool {
	for _, rt := range vmbasedRuntimes {
		if strings.Contains(runtimeType, rt) {
			return true
		}
	}
	return false
}

func modifyProcessLabel(runtimeType string, spec *specs.Spec) error {
	if !isVMBasedRuntime(runtimeType) {
		return nil
	}
	l, err := seutil.ChangeToKVM(spec.Process.SelinuxLabel)
	if err != nil {
		return fmt.Errorf("failed to get selinux kvm label: %w", err)
	}
	spec.Process.SelinuxLabel = l
	return nil
}

// getCgroupsMode returns cgropu mode.
// TODO: add build constraints to cgroups package and remove this helper
func isUnifiedCgroupsMode() bool {
	return cgroups.Mode() == cgroups.Unified
}
