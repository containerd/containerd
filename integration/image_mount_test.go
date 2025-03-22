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
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/opencontainers/selinux/go-selinux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	criruntime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestImageVolumeMount(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Only running on linux")
	}

	testImage := images.Get(images.Alpine)
	testMountImage := images.Get(images.Alpine)
	mountPath := "/image-mount"
	EnsureImageExists(t, testMountImage)
	EnsureImageExists(t, testImage)

	testcases := []struct {
		name         string
		subPath      string
		cmd          []string
		expectErr    bool
		expectOutput string
	}{
		{
			name:         "mount image (no subpath)",
			subPath:      "",
			cmd:          []string{"ls", filepath.Join(mountPath, "etc", "os-release")},
			expectErr:    false,
			expectOutput: filepath.Join(mountPath, "etc", "os-release"),
		},
		{
			// mount `etc` subpath to `mountPath`, then `etc/os-release` should be
			// available at `mountPath/os-release` within the container.
			name:         "mount etc (directory) subpath only",
			subPath:      "etc",
			cmd:          []string{"ls", filepath.Join(mountPath, "os-release")},
			expectErr:    false,
			expectOutput: filepath.Join(mountPath, "os-release"),
		},
		{
			// mount `etc/os-release` subpath (a file) to `mountPath`, then this file
			// should be available at `mountPath` within the container.
			// And we should only have that single file.
			name:         "mount etc/os-release (file) subpath only",
			subPath:      "etc/os-release",
			cmd:          []string{"ls", mountPath},
			expectErr:    false,
			expectOutput: mountPath,
		},
		{
			name:      "fail to mount non-existent subpath",
			subPath:   "non-existent-subpath",
			cmd:       []string{"ls", mountPath},
			expectErr: true,
		},
		{
			name:      "fail to mount absolute subpath",
			subPath:   "/etc",
			cmd:       []string{"ls", mountPath},
			expectErr: true,
		},
		{
			name:      "fail to mount escaped subpath",
			subPath:   "etc/../../..",
			cmd:       []string{"ls", mountPath},
			expectErr: true,
		},
		{
			name:      "fail to mount a symlink file that escapes subpath",
			subPath:   "bin/sh", // `bin/sh` is a symlink to `/bin/busybox` in the mount image
			cmd:       []string{"ls", mountPath},
			expectErr: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			testImageMount(t, testImage, testMountImage, tc.subPath, mountPath, "", tc.cmd, tc.expectErr, tc.expectOutput)
		})
	}
}

func TestImageMountSELinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Only running on linux")
	}

	if !selinux.GetEnabled() {
		t.Skip("SELinux is not enabled")
	}

	testImage := images.Get(images.ResourceConsumer)
	testMountImage := images.Get(images.Alpine)
	mountPath := "/image-mount"
	EnsureImageExists(t, testMountImage)
	EnsureImageExists(t, testImage)

	testcases := []struct {
		name         string
		subPath      string
		level        string
		cmd          []string
		expectErr    bool
		expectOutput string
	}{
		{
			name:         "mount image (no subpath) - s0:c4,c5",
			subPath:      "",
			level:        "s0:c4,c5",
			cmd:          []string{"ls", "-Z", filepath.Join(mountPath, "etc", "os-release")},
			expectErr:    false,
			expectOutput: fmt.Sprintf("system_u:object_r:container_file_t:s0:c4,c5 %s", filepath.Join(mountPath, "etc", "os-release")),
		},
		{
			name:         "mount image (no subpath) - s0:c200,c100",
			subPath:      "",
			level:        "s0:c200,c100",
			cmd:          []string{"ls", "-Z", filepath.Join(mountPath, "etc", "os-release")},
			expectErr:    false,
			expectOutput: fmt.Sprintf("system_u:object_r:container_file_t:s0:c100,c200 %s", filepath.Join(mountPath, "etc", "os-release")),
		},
		{
			name:         "mount etc/os-release (file) subpath",
			subPath:      "etc/os-release",
			level:        "s0:c4,c5",
			cmd:          []string{"ls", "-Z", mountPath},
			expectErr:    false,
			expectOutput: fmt.Sprintf("system_u:object_r:container_file_t:s0:c4,c5 %s", mountPath),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			testImageMount(t, testImage, testMountImage, tc.subPath, mountPath, tc.level, tc.cmd, tc.expectErr, tc.expectOutput)
		})
	}
}

func testImageMount(t *testing.T, testImage, testMountImage, imageSubPath, mountPath, selinuxLevel string, cmd []string, expectErr bool, want string) {
	var (
		containerName = "test-image-mount-container"
	)

	testPodLogDir := t.TempDir()

	sandboxOpts := []PodSandboxOpts{
		WithHostNetwork,
		WithPodLogDirectory(testPodLogDir),
	}
	if len(selinuxLevel) > 0 {
		sandboxOpts = append(sandboxOpts, WithSelinuxLevel(selinuxLevel))
	}

	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox",
		"image-mount", sandboxOpts...,
	)

	containerConfig := ContainerConfig(
		containerName,
		testImage,
		WithCommand(cmd[0], cmd[1:]...),
		WithImageVolumeMount(testMountImage, imageSubPath, mountPath),
		WithLogPath(containerName),
	)

	cn, err := runtimeService.CreateContainer(sb, containerConfig, sbConfig)
	if expectErr {
		require.Error(t, err)
		return
	}

	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.RemoveContainer(cn))
	}()

	require.NoError(t, runtimeService.StartContainer(cn))

	require.NoError(t, Eventually(func() (bool, error) {
		s, err := runtimeService.ContainerStatus(cn)
		if err != nil {
			return false, err
		}
		if s.GetState() == criruntime.ContainerState_CONTAINER_EXITED {
			return true, nil
		}
		return false, nil
	}, time.Second, 30*time.Second))

	content, err := os.ReadFile(filepath.Join(testPodLogDir, containerName))
	assert.NoError(t, err)
	checkContainerLog(t, string(content), []string{
		fmt.Sprintf("%s %s %s", criruntime.Stdout, criruntime.LogTagFull, want),
	})
}
