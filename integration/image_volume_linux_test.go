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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	kernel "github.com/containerd/containerd/v2/pkg/kernelversion"
	"github.com/stretchr/testify/require"
)

func TestImageVolumeCheckVolatileOption(t *testing.T) {
	ok, _ := kernel.GreaterEqualThan(
		kernel.KernelVersion{
			Kernel: 5, Major: 10,
		},
	)
	if !ok {
		t.Skip("Skip since kernel version < 5.10")
	}

	containerImage := "ghcr.io/containerd/alpine:3.14.0"
	podCtx, _ := setupRunningContainerWithImageVolume(t, "", containerImage, containerImage, "/alpine")
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
