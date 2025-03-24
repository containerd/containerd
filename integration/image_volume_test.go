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
	"runtime"
	"testing"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/opencontainers/selinux/go-selinux"
	"github.com/stretchr/testify/require"
	criruntime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestImageVolumeBasic(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Only running on linux")
	}

	for _, tc := range []struct {
		name                              string
		containerImage                    string
		selinuxLevel                      string
		imageVolumeImage, imageVolumePath string

		execSyncCommands []string
		execSyncError    string
		execSyncOutput   string
	}{
		{
			name:             "should be readonly content",
			containerImage:   images.Get(images.Alpine),
			imageVolumeImage: images.Get(images.Pause),
			imageVolumePath:  "/image-mount",
			execSyncCommands: []string{"rm", "/image-mount/pause"},
			execSyncError:    "can't remove '/image-mount/pause': Read-only file system",
		},
		{
			name:             "should apply selinux labels - s0:c4,c5",
			containerImage:   images.Get(images.ResourceConsumer),
			selinuxLevel:     "s0:c4,c5",
			imageVolumeImage: images.Get(images.Pause),
			imageVolumePath:  "/image-mount",
			execSyncCommands: []string{"ls", "-Z", "/image-mount"},
			execSyncOutput:   "system_u:object_r:container_file_t:s0:c4,c5 pause",
		},
		{
			name:             "should apply selinux labels - s0:c200,c100",
			containerImage:   images.Get(images.ResourceConsumer),
			selinuxLevel:     "s0:c200,c100",
			imageVolumeImage: images.Get(images.Pause),
			imageVolumePath:  "/image-mount",
			execSyncCommands: []string{"ls", "-Z", "/image-mount"},
			execSyncOutput:   "system_u:object_r:container_file_t:s0:c100,c200 pause",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.selinuxLevel != "" {
				if !selinux.GetEnabled() {
					t.Skip("SELinux is not enabled")
				}
			}

			podCtx, cnID := setupRunningContainerWithImageVolume(t, tc.selinuxLevel, tc.containerImage, tc.imageVolumeImage, tc.imageVolumePath)
			t.Cleanup(func() {
				podCtx.stop(true)
			})

			stdout, stderr, err := runtimeService.ExecSync(cnID, tc.execSyncCommands, 0)
			if tc.execSyncError != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tc.execSyncError)
				return
			}

			require.NoError(t, err)
			require.Len(t, stderr, 0)
			require.Contains(t, string(stdout), tc.execSyncOutput)
		})
	}
}

func setupRunningContainerWithImageVolume(t *testing.T, selinuxLevel string, containerImage string, imageVolumeName, imageVolumePath string) (*podTCtx, string) {
	podLogDir := t.TempDir()

	podOpts := []PodSandboxOpts{
		WithPodLogDirectory(podLogDir),
	}
	if selinuxLevel != "" {
		podOpts = append(podOpts, WithSelinuxLevel(selinuxLevel))
	}
	podCtx := newPodTCtx(t, runtimeService, t.Name(), "image-voloume", podOpts...)
	defer func() {
		if t.Failed() {
			podCtx.stop(true)
		}
	}()

	pullImagesByCRI(t, imageService, containerImage, imageVolumeName)

	containerName := "running"
	cnID := podCtx.createContainer(containerName,
		containerImage,
		criruntime.ContainerState_CONTAINER_RUNNING,
		WithCommand("sleep", "1d"),
		WithImageVolumeMount(imageVolumeName, imageVolumePath),
		WithLogPath(containerName))
	return podCtx, cnID
}
