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

package podsandbox

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/pkg/seccomp"
	"github.com/containerd/containerd/pkg/seutil"
	"github.com/moby/sys/mountinfo"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/selinux/go-selinux/label"
	"golang.org/x/sys/unix"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const (
	// defaultSandboxOOMAdj is default omm adj for sandbox container. (kubernetes#47938).
	defaultSandboxOOMAdj = -998
	// defaultShmSize is the default size of the sandbox shm.
	defaultShmSize = int64(1024 * 1024 * 64)
	// relativeRootfsPath is the rootfs path relative to bundle path.
	relativeRootfsPath = "rootfs"
	// devShm is the default path of /dev/shm.
	devShm = "/dev/shm"
	// etcHosts is the default path of /etc/hosts file.
	etcHosts = "/etc/hosts"
	// resolvConfPath is the abs path of resolv.conf on host or container.
	resolvConfPath = "/etc/resolv.conf"
)

// getCgroupsPath generates container cgroups path.
func getCgroupsPath(cgroupsParent, id string) string {
	base := path.Base(cgroupsParent)
	if strings.HasSuffix(base, ".slice") {
		// For a.slice/b.slice/c.slice, base is c.slice.
		// runc systemd cgroup path format is "slice:prefix:name".
		return strings.Join([]string{base, "cri-containerd", id}, ":")
	}
	return filepath.Join(cgroupsParent, id)
}

// getSandboxHostname returns the hostname file path inside the sandbox root directory.
func (c *Controller) getSandboxHostname(id string) string {
	return filepath.Join(c.getSandboxRootDir(id), "hostname")
}

// getSandboxHosts returns the hosts file path inside the sandbox root directory.
func (c *Controller) getSandboxHosts(id string) string {
	return filepath.Join(c.getSandboxRootDir(id), "hosts")
}

// getResolvPath returns resolv.conf filepath for specified sandbox.
func (c *Controller) getResolvPath(id string) string {
	return filepath.Join(c.getSandboxRootDir(id), "resolv.conf")
}

// getSandboxDevShm returns the shm file path inside the sandbox root directory.
func (c *Controller) getSandboxDevShm(id string) string {
	return filepath.Join(c.getVolatileSandboxRootDir(id), "shm")
}

func toLabel(selinuxOptions *runtime.SELinuxOption) ([]string, error) {
	var labels []string

	if selinuxOptions == nil {
		return nil, nil
	}
	if err := checkSelinuxLevel(selinuxOptions.Level); err != nil {
		return nil, err
	}
	if selinuxOptions.User != "" {
		labels = append(labels, "user:"+selinuxOptions.User)
	}
	if selinuxOptions.Role != "" {
		labels = append(labels, "role:"+selinuxOptions.Role)
	}
	if selinuxOptions.Type != "" {
		labels = append(labels, "type:"+selinuxOptions.Type)
	}
	if selinuxOptions.Level != "" {
		labels = append(labels, "level:"+selinuxOptions.Level)
	}

	return labels, nil
}

func initLabelsFromOpt(selinuxOpts *runtime.SELinuxOption) (string, string, error) {
	labels, err := toLabel(selinuxOpts)
	if err != nil {
		return "", "", err
	}
	return label.InitLabels(labels)
}

func checkSelinuxLevel(level string) error {
	if len(level) == 0 {
		return nil
	}

	matched, err := regexp.MatchString(`^s\d(-s\d)??(:c\d{1,4}(\.c\d{1,4})?(,c\d{1,4}(\.c\d{1,4})?)*)?$`, level)
	if err != nil {
		return fmt.Errorf("the format of 'level' %q is not correct: %w", level, err)
	}
	if !matched {
		return fmt.Errorf("the format of 'level' %q is not correct", level)
	}
	return nil
}

func (c *Controller) seccompEnabled() bool {
	return seccomp.IsEnabled()
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
