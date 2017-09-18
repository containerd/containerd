/*
Copyright 2017 The Kubernetes Authors.

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
	"io/ioutil"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
)

func TestImageFSInfo(t *testing.T) {
	t.Logf("Pull an image to make sure image fs is not empty")
	img, err := imageService.PullImage(&runtime.ImageSpec{Image: "busybox"}, nil)
	require.NoError(t, err)
	defer func() {
		err := imageService.RemoveImage(&runtime.ImageSpec{Image: img})
		assert.NoError(t, err)
	}()
	t.Logf("Create a sandbox to make sure there is an active snapshot")
	config := PodSandboxConfig("running-pod", "imagefs")
	sb, err := runtimeService.RunPodSandbox(config)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.StopPodSandbox(sb))
		assert.NoError(t, runtimeService.RemovePodSandbox(sb))
	}()

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
			info.GetInodesUsed().GetValue() != 0 &&
			info.GetStorageId().GetUuid() != "" {
			return true, nil
		}
		return false, nil
	}, time.Second, 30*time.Second))

	t.Logf("Device uuid should exist")
	files, err := ioutil.ReadDir("/dev/disk/by-uuid")
	require.NoError(t, err)
	var names []string
	for _, f := range files {
		names = append(names, f.Name())
	}
	assert.Contains(t, names, info.GetStorageId().GetUuid())
}
