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

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	runc "github.com/containerd/go-runc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	exec "golang.org/x/sys/execabs"
	"golang.org/x/sys/unix"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const (
	defaultRoot = "/var/lib/containerd-test"
)

func supportsUserNS() bool {
	if _, err := os.Stat("/proc/self/ns/user"); os.IsNotExist(err) {
		return false
	}
	return true
}

func supportsIDMap(path string) bool {
	treeFD, err := unix.OpenTree(-1, path, uint(unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC))
	if err != nil {
		return false
	}
	defer unix.Close(treeFD)

	// We want to test if idmap mounts are supported.
	// So we use just some random mapping, it doesn't really matter which one.
	// For the helper command, we just need something that is alive while we
	// test this, a sleep 5 will do it.
	cmd := exec.Command("sleep", "5")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:  syscall.CLONE_NEWUSER,
		UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: 65536, Size: 65536}},
		GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: 65536, Size: 65536}},
	}
	if err := cmd.Start(); err != nil {
		return false
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	usernsFD := fmt.Sprintf("/proc/%d/ns/user", cmd.Process.Pid)
	var usernsFile *os.File
	if usernsFile, err = os.Open(usernsFD); err != nil {
		return false
	}
	defer usernsFile.Close()

	attr := unix.MountAttr{
		Attr_set:  unix.MOUNT_ATTR_IDMAP,
		Userns_fd: uint64(usernsFile.Fd()),
	}
	if err := unix.MountSetattr(treeFD, "", unix.AT_EMPTY_PATH, &attr); err != nil {
		return false
	}

	return true
}

// traversePath gives 755 permissions for all elements in tPath below
// os.TempDir() and errors out if elements above it don't have read+exec
// permissions for others.  tPath MUST be a descendant of os.TempDir(). The path
// returned by testing.TempDir() usually is.
func traversePath(tPath string) error {
	// Check the assumption that the argument is under os.TempDir().
	tempBase := os.TempDir()
	if !strings.HasPrefix(tPath, tempBase) {
		return fmt.Errorf("traversePath: %q is not a descendant of %q", tPath, tempBase)
	}

	var path string
	for _, p := range strings.SplitAfter(tPath, "/") {
		path = path + p
		stats, err := os.Stat(path)
		if err != nil {
			return err
		}

		perm := stats.Mode().Perm()
		if perm&0o5 == 0o5 {
			continue
		}
		if strings.HasPrefix(tempBase, path) {
			return fmt.Errorf("traversePath: directory %q MUST have read+exec permissions for others", path)
		}

		if err := os.Chmod(path, perm|0o755); err != nil {
			return err
		}
	}

	return nil
}

func TestPodUserNS(t *testing.T) {
	containerID := uint32(0)
	hostID := uint32(65536)
	size := uint32(65536)
	idmap := []*runtime.IDMapping{
		{
			ContainerId: containerID,
			HostId:      hostID,
			Length:      size,
		},
	}

	volumeHostPath := t.TempDir()
	if err := traversePath(volumeHostPath); err != nil {
		t.Fatalf("failed to setup volume host path: %v", err)
	}

	for name, test := range map[string]struct {
		sandboxOpts   []PodSandboxOpts
		containerOpts []ContainerOpts
		checkOutput   func(t *testing.T, output string)
		hostVolumes   bool // whether to config uses host Volumes
		expectErr     bool
	}{
		"userns uid mapping": {
			sandboxOpts: []PodSandboxOpts{
				WithPodUserNs(containerID, hostID, size),
			},
			containerOpts: []ContainerOpts{
				WithUserNamespace(containerID, hostID, size),
				WithCommand("cat", "/proc/self/uid_map"),
			},
			checkOutput: func(t *testing.T, output string) {
				// The output should contain the length of the userns requested.
				assert.Contains(t, output, fmt.Sprint(size))
			},
		},
		"userns gid mapping": {
			sandboxOpts: []PodSandboxOpts{
				WithPodUserNs(containerID, hostID, size),
			},
			containerOpts: []ContainerOpts{
				WithUserNamespace(containerID, hostID, size),
				WithCommand("cat", "/proc/self/gid_map"),
			},
			checkOutput: func(t *testing.T, output string) {
				// The output should contain the length of the userns requested.
				assert.Contains(t, output, fmt.Sprint(size))
			},
		},
		"rootfs permissions": {
			sandboxOpts: []PodSandboxOpts{
				WithPodUserNs(containerID, hostID, size),
			},
			containerOpts: []ContainerOpts{
				WithUserNamespace(containerID, hostID, size),
				// Prints numeric UID and GID for path.
				// For example, if UID and GID is 0 it will print: =0=0=
				// We add the "=" signs so we use can assert.Contains() and be sure
				// the UID/GID is 0 and not things like 100 (that contain 0).
				// We can't use assert.Equal() easily as it contains timestamp, etc.
				WithCommand("stat", "-c", "'=%u=%g='", "/root/"),
			},
			checkOutput: func(t *testing.T, output string) {
				// The UID and GID should be 0 (root) if the chown/remap is done correctly.
				assert.Contains(t, output, "=0=0=")
			},
		},
		"volumes permissions": {
			sandboxOpts: []PodSandboxOpts{
				WithPodUserNs(containerID, hostID, size),
			},
			hostVolumes: true,
			containerOpts: []ContainerOpts{
				WithUserNamespace(containerID, hostID, size),
				WithIDMapVolumeMount(volumeHostPath, "/mnt", idmap, idmap),
				// Prints numeric UID and GID for path.
				// For example, if UID and GID is 0 it will print: =0=0=
				// We add the "=" signs so we use can assert.Contains() and be sure
				// the UID/GID is 0 and not things like 100 (that contain 0).
				// We can't use assert.Equal() easily as it contains timestamp, etc.
				WithCommand("stat", "-c", "'=%u=%g='", "/mnt/"),
			},
			checkOutput: func(t *testing.T, output string) {
				// The UID and GID should be the current user if chown/remap is done correctly.
				uid := "0"
				user, err := user.Current()
				if user != nil && err == nil {
					uid = user.Uid
				}
				assert.Contains(t, output, "="+uid+"="+uid+"=")
			},
		},
		"fails with several mappings": {
			sandboxOpts: []PodSandboxOpts{
				WithPodUserNs(containerID, hostID, size),
				WithPodUserNs(containerID*2, hostID*2, size*2),
			},
			expectErr: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if !supportsUserNS() {
				t.Skip("User namespaces are not supported")
			}
			if !supportsIDMap(defaultRoot) {
				t.Skipf("ID mappings are not supported on: %v", defaultRoot)
			}
			if test.hostVolumes && !supportsIDMap(volumeHostPath) {
				t.Skipf("ID mappings are not supported host volume filesystem: %v", volumeHostPath)
			}
			if err := supportsRuncIDMap(); err != nil {
				t.Skipf("OCI runtime doesn't support idmap mounts: %v", err)
			}

			testPodLogDir := t.TempDir()
			sandboxOpts := append(test.sandboxOpts, WithPodLogDirectory(testPodLogDir))
			t.Log("Create a sandbox with userns")
			sbConfig := PodSandboxConfig("sandbox", "userns", sandboxOpts...)
			sb, err := runtimeService.RunPodSandbox(sbConfig, *runtimeHandler)
			if err != nil {
				if !test.expectErr {
					t.Fatalf("Unexpected RunPodSandbox error: %v", err)
				}
				return
			}
			// Make sure the sandbox is cleaned up.
			defer func() {
				assert.NoError(t, runtimeService.StopPodSandbox(sb))
				assert.NoError(t, runtimeService.RemovePodSandbox(sb))
			}()
			if test.expectErr {
				t.Fatalf("Expected RunPodSandbox to return error")
			}

			var (
				testImage     = images.Get(images.BusyBox)
				containerName = "test-container"
			)

			EnsureImageExists(t, testImage)

			containerOpts := append(test.containerOpts,
				WithLogPath(containerName),
			)
			t.Log("Create a container for userns")
			cnConfig := ContainerConfig(
				containerName,
				testImage,
				containerOpts...,
			)
			cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
			require.NoError(t, err)

			t.Log("Start the container")
			require.NoError(t, runtimeService.StartContainer(cn))

			t.Log("Wait for container to finish running")
			require.NoError(t, Eventually(func() (bool, error) {
				s, err := runtimeService.ContainerStatus(cn)
				if err != nil {
					return false, err
				}
				if s.GetState() == runtime.ContainerState_CONTAINER_EXITED {
					return true, nil
				}
				return false, nil
			}, time.Second, 30*time.Second))

			content, err := os.ReadFile(filepath.Join(testPodLogDir, containerName))
			assert.NoError(t, err)

			t.Log("Running check function")
			test.checkOutput(t, string(content))
		})
	}
}

func supportsRuncIDMap() error {
	var r runc.Runc
	features, err := r.Features(context.Background())
	if err != nil {
		// If the features command is not implemented, then runc is too old.
		return fmt.Errorf("features command failed: %w", err)
	}

	if features.Linux.MountExtensions == nil || features.Linux.MountExtensions.IDMap == nil {
		return errors.New("missing `mountExtensions.idmap` entry in `features` command")

	}
	if enabled := features.Linux.MountExtensions.IDMap.Enabled; enabled == nil || !*enabled {
		return errors.New("idmap mounts not supported")
	}

	return nil
}
