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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestImageFSInfo(t *testing.T) {
	t.Logf("Create a sandbox to make sure there is an active snapshot")
	PodSandboxConfigWithCleanup(t, "running-pod", "imagefs")

	t.Logf("Pull an image to make sure image fs is not empty")
	EnsureImageExists(t, GetImage(BusyBox))

	// It takes time to populate imagefs stats. Use eventually
	// to check for a period of time.
	t.Logf("Check imagefs info")
	var info *runtime.FilesystemUsage
	require.NoError(t, Eventually(func() (bool, error) {
		stats, err := imageService.ImageFsInfo()
		if err != nil {
			return false, err
		}
		if len(stats) == 0 {
			return false, nil
		}
		if len(stats) >= 2 {
			return false, fmt.Errorf("unexpected stats length: %d", len(stats))
		}
		info = stats[0]
		if info.GetTimestamp() != 0 &&
			info.GetUsedBytes().GetValue() != 0 &&
			info.GetFsId().GetMountpoint() != "" {
			return true, nil
		}
		return false, nil
	}, time.Second, 30*time.Second))

	t.Logf("Image filesystem mountpath should exist")
	_, err := os.Stat(info.GetFsId().GetMountpoint())
	assert.NoError(t, err)
}
