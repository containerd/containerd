//go:build windows

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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio/pkg/bindfilter"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/opencontainers/image-spec/identity"
	"github.com/stretchr/testify/require"
)

const windowsTestImage = "ghcr.io/containerd/windows/nanoserver:ltsc2022"

// isBindFilterActive returns true if the given path has an active bind filter
// mapping in the Windows kernel. This is an independent reimplementation of the same
// check used by ensureImageVolumeMounted in internal/cri/server/container_image_mount_windows.go.
func isBindFilterActive(t *testing.T, target string) bool {
	t.Helper()
	// Normalize path before comparing against bind filter mount points.
	target = filepath.Clean(target)
	volume := filepath.VolumeName(target) + string(filepath.Separator)
	mappings, err := bindfilter.GetBindMappings(volume)
	if err != nil {
		// GetBindMappings can fail if stale bind filter entries reference
		// VHD disks that are no longer mounted. Treat this as not mounted.
		t.Logf("GetBindMappings returned error (treating as not mounted): %v", err)
		return false
	}
	// Windows paths are case-insensitive; use EqualFold for comparison.
	for _, m := range mappings {
		if strings.EqualFold(filepath.Clean(m.MountPoint), target) {
			return true
		}
	}
	return false
}

// criStateDir returns the CRI plugin state directory from the running containerd.
// The state directory is where the CRI plugin stores pod and container state,
// including image volume mounts at <stateDir>/image-volumes/<podID>/<imageDigest>.
//
// This mirrors criRuntimeInfo in release_upgrade_linux_test.go, which parses
// the same runtime status JSON to extract configuration values.
func criStateDir(t *testing.T) string {
	t.Helper()
	resp, err := runtimeService.Status()
	require.NoError(t, err)
	rawCfg, ok := resp.Info["config"]
	require.True(t, ok, "could not find config in containerd runtime info")
	var cfg map[string]any
	require.NoError(t, json.Unmarshal([]byte(rawCfg), &cfg))
	stateDir, ok := cfg["stateDir"].(string)
	require.True(t, ok && stateDir != "", "could not find stateDir in containerd runtime info")
	return stateDir
}

// TestImageVolumeBasicWindows verifies that an OCI image can be mounted as a
// read-only volume and its contents are accessible inside the container.
func TestImageVolumeBasicWindows(t *testing.T) {
	EnsureImageExists(t, windowsTestImage)

	// Create a pod sandbox — the container will live inside this.
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "image-volume-windows")

	// Configure a container that mounts windowsTestImage as a read-only volume
	// at C:\image-mount, then keeps running with a ping loop.
	cfg := ContainerConfig("test-container", windowsTestImage,
		WithCommand("cmd", "/c", "ping -t 127.0.0.1"),
		WithImageVolumeMount(windowsTestImage, "", `C:\image-mount`),
	)
	cnID, err := runtimeService.CreateContainer(sb, cfg, sbConfig)
	require.NoError(t, err)
	t.Cleanup(func() {
		runtimeService.StopContainer(cnID, 10)
		runtimeService.RemoveContainer(cnID)
	})
	require.NoError(t, runtimeService.StartContainer(cnID))

	// Verify the mount path has content. nanoserver contains License.txt,
	// Users\, and Windows\ — a non-empty dir listing confirms the mount worked.
	stdout, stderr, err := runtimeService.ExecSync(cnID,
		[]string{"cmd", "/c", "dir C:\\image-mount"},
		30*time.Second)
	t.Logf("stdout: %s", stdout)
	t.Logf("stderr: %s", stderr)
	require.NoError(t, err)
	require.NotEmpty(t, string(stdout), "expected files at C:\\image-mount, got empty output")
}

// TestImageVolumeSetupIfContainerdRestartsWindows verifies that ensureImageVolumeMounted
// correctly handles two scenarios:
//
//  1. The image volume directory exists but has no active bind filter (simulates
//     a containerd restart where the bind filter was removed by the OS but the
//     directory remains on disk). The volume must be remounted.
//
//  2. The image volume directory exists and has an active bind filter (normal
//     "second container in same pod" case). The volume must not be remounted.
//
// This mirrors TestImageVolumeSetupIfContainerdRestarts in image_volume_linux_test.go.
func TestImageVolumeSetupIfContainerdRestartsWindows(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "k8s.io")

	EnsureImageExists(t, windowsTestImage)

	// Resolve the image's chain ID and digest — these are used to locate the
	// snapshot and the image volume directory path respectively.
	img, err := containerdClient.GetImage(ctx, windowsTestImage)
	require.NoError(t, err)

	diffIDs, err := img.RootFS(ctx)
	require.NoError(t, err)

	imageChainID := identity.ChainID(diffIDs).String()
	imageTarget := img.Target().Digest.Encoded()

	snSrv := containerdClient.SnapshotService("windows")
	stateDir := criStateDir(t)

	t.Run("snapshot and dir exist, no bind filter (restart scenario)", func(t *testing.T) {
		// Simulates a containerd restart: snapshot exists, directory exists,
		// but the bind filter is gone. ensureImageVolumeMounted must detect
		// the missing bind filter and remount.
		sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "image-volume-restart-windows")

		// target is the path that mutateImageMount would use for this pod and image.
		target := filepath.Join(stateDir, "image-volumes", sb, imageTarget)

		// Pre-create the snapshot and directory without applying a bind filter.
		// This replicates the on-disk state after a containerd restart: the
		// snapshot and directory persist, but the kernel bind filter is gone.
		_, err := snSrv.Prepare(ctx, target, imageChainID)
		require.NoError(t, err)
		t.Cleanup(func() {
			snSrv.Remove(ctx, target)
			os.RemoveAll(target)
		})
		require.NoError(t, os.MkdirAll(target, 0755))

		require.False(t, isBindFilterActive(t, target),
			"bind filter should not be active before container creation")

		cfg := ContainerConfig(t.Name()+"-restart-cn",
			windowsTestImage,
			WithCommand("cmd", "/c", "ping -t 127.0.0.1"),
			WithImageVolumeMount(windowsTestImage, "", `C:\image-mount`),
		)
		cnID, err := runtimeService.CreateContainer(sb, cfg, sbConfig)
		require.NoError(t, err)
		t.Cleanup(func() {
			runtimeService.StopContainer(cnID, 10)
			runtimeService.RemoveContainer(cnID)
		})
		require.NoError(t, runtimeService.StartContainer(cnID))

		// After container creation, ensureImageVolumeMounted should have detected
		// the missing bind filter and triggered a remount.
		require.True(t, isBindFilterActive(t, target),
			"bind filter should be active after container creation (remounted)")
	})

	t.Run("bind filter active after first container (idempotency)", func(t *testing.T) {
		// Normal case: first container mounts the volume, second container in
		// the same pod should reuse the existing mount without remounting.
		sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "image-volume-idempotency-windows")
		target := filepath.Join(stateDir, "image-volumes", sb, imageTarget)

		// First container — this triggers mutateImageMount which calls
		// ensureImageVolumeMounted, finds no existing mount, and applies the bind filter.
		cn1, err := runtimeService.CreateContainer(sb, ContainerConfig("first",
			windowsTestImage,
			WithCommand("cmd", "/c", "ping -t 127.0.0.1"),
			WithImageVolumeMount(windowsTestImage, "", `C:\image-mount`),
		), sbConfig)
		require.NoError(t, err)
		t.Cleanup(func() {
			runtimeService.StopContainer(cn1, 10)
			runtimeService.RemoveContainer(cn1)
		})
		require.NoError(t, runtimeService.StartContainer(cn1))

		require.True(t, isBindFilterActive(t, target),
			"bind filter should be active after first container creation")

		// Second container in the same pod — ensureImageVolumeMounted should
		// detect the active bind filter via GetBindMappings and skip remounting.
		cn2, err := runtimeService.CreateContainer(sb, ContainerConfig("second",
			windowsTestImage,
			WithCommand("cmd", "/c", "ping -t 127.0.0.1"),
			WithImageVolumeMount(windowsTestImage, "", `C:\image-mount`),
		), sbConfig)
		require.NoError(t, err)
		t.Cleanup(func() {
			runtimeService.StopContainer(cn2, 10)
			runtimeService.RemoveContainer(cn2)
		})
		require.NoError(t, runtimeService.StartContainer(cn2))

		require.True(t, isBindFilterActive(t, target),
			"bind filter should still be active after second container creation (not remounted)")
	})
}
