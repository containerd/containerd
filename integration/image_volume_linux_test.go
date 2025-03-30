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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/integration/images"
	kernel "github.com/containerd/containerd/v2/pkg/kernelversion"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	"github.com/opencontainers/image-spec/identity"
	"github.com/opencontainers/selinux/go-selinux"
	"github.com/stretchr/testify/require"
	criruntime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestImageVolumeBasic(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "k8s.io")

	snSrv := containerdClient.SnapshotService("overlayfs")
	for _, tc := range []struct {
		name                           string
		containerImage                 string
		selinuxLevel                   string
		imageVolumeImage, imageSubPath string
		containerPath                  string

		createContainerError string
		execSyncCommands     []string
		execSyncError        string
		execSyncOutput       string
	}{
		{
			name:             "should be readonly content",
			containerImage:   images.Get(images.Alpine),
			imageVolumeImage: images.Get(images.Pause),
			containerPath:    "/image-mount",
			execSyncCommands: []string{"rm", "/image-mount/pause"},
			execSyncError:    "can't remove '/image-mount/pause': Read-only file system",
		},
		{
			name:             "should apply selinux labels - s0:c4,c5",
			containerImage:   images.Get(images.ResourceConsumer),
			selinuxLevel:     "s0:c4,c5",
			imageVolumeImage: images.Get(images.Pause),
			containerPath:    "/image-mount",
			execSyncCommands: []string{"ls", "-Z", "/image-mount"},
			execSyncOutput:   "system_u:object_r:container_file_t:s0:c4,c5 pause",
		},
		{
			name:             "should apply selinux labels - s0:c200,c100",
			containerImage:   images.Get(images.ResourceConsumer),
			selinuxLevel:     "s0:c200,c100",
			imageVolumeImage: images.Get(images.Pause),
			containerPath:    "/image-mount",
			execSyncCommands: []string{"ls", "-Z", "/image-mount"},
			execSyncOutput:   "system_u:object_r:container_file_t:s0:c100,c200 pause",
		},
		{
			name:             "should only mount image subpath",
			containerImage:   images.Get(images.Alpine),
			imageVolumeImage: images.Get(images.Alpine),
			imageSubPath:     "etc",
			containerPath:    "/image-mount",
			execSyncCommands: []string{"ls", filepath.Join("/image-mount", "os-release")},
			execSyncOutput:   filepath.Join("/image-mount", "os-release"),
		},
		{
			name:                 "fail to mount single file subpath",
			containerImage:       images.Get(images.Alpine),
			imageVolumeImage:     images.Get(images.Pause),
			imageSubPath:         "pause",
			containerPath:        "/image-mount",
			createContainerError: "only directory subpath is supported",
		},
		{
			name:                 "fail to mount non-existent subpath",
			containerImage:       images.Get(images.Alpine),
			imageVolumeImage:     images.Get(images.Alpine),
			imageSubPath:         "non-existent-subpath",
			containerPath:        "/image-mount",
			createContainerError: "no such file or directory",
		},
		{
			name:                 "fail to mount absolute subpath",
			containerImage:       images.Get(images.Alpine),
			imageVolumeImage:     images.Get(images.Alpine),
			imageSubPath:         "/etc",
			containerPath:        "/image-mount",
			createContainerError: "path escapes from parent",
		},
		{
			name:                 "fail to mount escaped subpath",
			containerImage:       images.Get(images.Alpine),
			imageVolumeImage:     images.Get(images.Alpine),
			imageSubPath:         "etc/../../..",
			containerPath:        "/image-mount",
			createContainerError: "path escapes from parent",
		},
		{
			name:                 "fail to mount a symlink file that escapes subpath",
			containerImage:       images.Get(images.Alpine),
			imageVolumeImage:     images.Get(images.Alpine),
			imageSubPath:         "bin/sh", // `bin/sh` is a symlink to `/bin/busybox` in the mount image
			containerPath:        "/image-mount",
			createContainerError: "path escapes from parent",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.selinuxLevel != "" {
				if !selinux.GetEnabled() {
					t.Skip("SELinux is not enabled")
				}
			}

			podCtx, cnID, err := setupRunningContainerWithImageVolume(t, tc.selinuxLevel, tc.containerImage, tc.imageVolumeImage, tc.imageSubPath, tc.containerPath)
			if err != nil {
				require.NotEmpty(t, tc.createContainerError)
				require.Contains(t, err.Error(), tc.createContainerError)
				return
			}
			require.Empty(t, tc.createContainerError)

			cleanup := true
			defer func() {
				if cleanup {
					podCtx.stop(true)
				}
			}()

			volumeImg, err := containerdClient.GetImage(ctx, tc.imageVolumeImage)
			require.NoError(t, err)

			volumeImgTarget := filepath.Join(podCtx.imageVolumeDir(), volumeImg.Target().Digest.Encoded())
			_, err = snSrv.Mounts(ctx, volumeImgTarget)
			require.NoError(t, err)

			defer func() {
				podCtx.stop(true)

				cleanup = false

				t.Log("Check snapshot after deleting pod")
				for range 30 {
					_, err := snSrv.Mounts(ctx, volumeImgTarget)
					if errdefs.IsNotFound(err) {
						return
					}
					time.Sleep(1 * time.Second)
				}
				t.Fatalf("%s should be deleted", volumeImgTarget)
			}()

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

func setupRunningContainerWithImageVolume(t *testing.T, selinuxLevel string, containerImage string, imageVolumeName, imageSubPath, containerPath string) (podCtx *podTCtx, cnID string, err error) {
	podLogDir := t.TempDir()

	podOpts := []PodSandboxOpts{
		WithPodLogDirectory(podLogDir),
	}
	if selinuxLevel != "" {
		podOpts = append(podOpts, WithSelinuxLevel(selinuxLevel))
	}
	podCtx = newPodTCtx(t, runtimeService, t.Name(), "image-voloume", podOpts...)
	defer func() {
		if t.Failed() || err != nil {
			podCtx.stop(true)
		}
	}()

	pullImagesByCRI(t, imageService, containerImage, imageVolumeName)

	containerName := "running"
	cfg := ContainerConfig(containerName, containerImage,
		WithCommand("sleep", "1d"),
		WithImageVolumeMount(imageVolumeName, imageSubPath, containerPath),
		WithLogPath(containerName),
	)
	cnID, err = podCtx.rSvc.CreateContainer(podCtx.id, cfg, podCtx.cfg)
	if err != nil {
		return podCtx, "", err
	}

	require.NoError(t, podCtx.rSvc.StartContainer(cnID))
	return podCtx, cnID, nil
}

func TestImageVolumeCheckVolatileOption(t *testing.T) {
	ok, _ := kernel.GreaterEqualThan(
		kernel.KernelVersion{
			Kernel: 5, Major: 10,
		},
	)
	if !ok {
		t.Skip("Skip since kernel version < 5.10")
	}

	containerImage := images.Get(images.Alpine)
	podCtx, _, err := setupRunningContainerWithImageVolume(t, "", containerImage, containerImage, "", "/alpine")
	require.NoError(t, err)
	t.Cleanup(func() {
		podCtx.stop(true)
	})

	imageVolumeDir := podCtx.imageVolumeDir()
	entries, err := os.ReadDir(imageVolumeDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	imageVolumeMount := filepath.Join(imageVolumeDir, entries[0].Name())
	mpInfo, err := mount.Lookup(imageVolumeMount)
	require.NoError(t, err)
	require.Equal(t, mpInfo.Mountpoint, imageVolumeMount)
	require.Equal(t, "overlay", mpInfo.FSType)
	require.Contains(t, strings.Split(mpInfo.VFSOptions, ","), "volatile")
}

func TestImageVolumeSetupIfContainerdRestarts(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "k8s.io")

	alpineImage := images.Get(images.Alpine)
	pullImagesByCRI(t, imageService, alpineImage)

	img, err := containerdClient.GetImage(ctx, alpineImage)
	require.NoError(t, err)

	diffIDs, err := img.RootFS(ctx)
	require.NoError(t, err)

	alpineImageChainID := identity.ChainID(diffIDs).String()
	alpineImageTarget := img.Target().Digest.Encoded()

	snSrv := containerdClient.SnapshotService("overlayfs")
	for _, tc := range []struct {
		name                  string
		beforeCreateContainer func(t *testing.T, podCtx *podTCtx)
	}{
		{
			name: "create target snapshot first",
			beforeCreateContainer: func(t *testing.T, podCtx *podTCtx) {
				target := filepath.Join(podCtx.imageVolumeDir(), alpineImageTarget)

				_, err := snSrv.Prepare(ctx, target, alpineImageChainID)
				require.NoError(t, err)
			},
		},
		{
			name: "create target snapshot/dir first",
			beforeCreateContainer: func(t *testing.T, podCtx *podTCtx) {
				target := filepath.Join(podCtx.imageVolumeDir(), alpineImageTarget)

				_, err := snSrv.Prepare(ctx, target, alpineImageChainID)
				require.NoError(t, err)

				require.NoError(t, os.MkdirAll(target, 0755))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			podCtx := newPodTCtx(t, runtimeService, t.Name(), "image-voloume")

			defer func() {
				podCtx.stop(true)
			}()

			targetVolumeMount := filepath.Join(podCtx.imageVolumeDir(), alpineImageTarget)

			tc.beforeCreateContainer(t, podCtx)

			podCtx.createContainer("running-1",
				alpineImage,
				criruntime.ContainerState_CONTAINER_RUNNING,
				WithCommand("sleep", "1d"),
				WithImageVolumeMount(alpineImage, "", "/alpine-2"))

			mpInfo1, err := mount.Lookup(targetVolumeMount)
			require.NoError(t, err)
			require.Equal(t, targetVolumeMount, mpInfo1.Mountpoint)

			podCtx.createContainer("running-2",
				alpineImage,
				criruntime.ContainerState_CONTAINER_RUNNING,
				WithCommand("sleep", "1d"),
				WithImageVolumeMount(alpineImage, "", "/alpine-2"))

			mpInfo2, err := mount.Lookup(targetVolumeMount)
			require.NoError(t, err)

			require.Equal(t, mpInfo1, mpInfo2, "should not mount twice")
		})
	}
}
