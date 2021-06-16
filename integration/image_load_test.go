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
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

// Test to load an image from tarball.
func TestImageLoad(t *testing.T) {
	testImage := GetImage(BusyBox)
	loadedImage := testImage
	_, err := exec.LookPath("docker")
	if err != nil {
		t.Skipf("Docker is not available: %v", err)
	}
	t.Logf("docker save image into tarball")
	output, err := exec.Command("docker", "pull", testImage).CombinedOutput()
	require.NoError(t, err, "output: %q", output)
	// ioutil.TempFile also opens a file, which might prevent us from overwriting that file with docker save.
	tarDir, err := ioutil.TempDir("", "image-load")
	tar := filepath.Join(tarDir, "image.tar")
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, os.RemoveAll(tarDir))
	}()
	output, err = exec.Command("docker", "save", testImage, "-o", tar).CombinedOutput()
	require.NoError(t, err, "output: %q", output)

	t.Logf("make sure no such image in cri")
	img, err := imageService.ImageStatus(&runtime.ImageSpec{Image: testImage})
	require.NoError(t, err)
	if img != nil {
		require.NoError(t, imageService.RemoveImage(&runtime.ImageSpec{Image: testImage}))
	}

	t.Logf("load image in cri")
	ctr, err := exec.LookPath("ctr")
	require.NoError(t, err, "ctr should be installed, make sure you've run `make install.deps`")
	output, err = exec.Command(ctr, "-address="+containerdEndpoint,
		"-n=k8s.io", "images", "import", tar).CombinedOutput()
	require.NoError(t, err, "output: %q", output)

	t.Logf("make sure image is loaded")
	// Use Eventually because the cri plugin needs a short period of time
	// to pick up images imported into containerd directly.
	require.NoError(t, Eventually(func() (bool, error) {
		img, err = imageService.ImageStatus(&runtime.ImageSpec{Image: testImage})
		if err != nil {
			return false, err
		}
		return img != nil, nil
	}, 100*time.Millisecond, 10*time.Second))
	require.Equal(t, []string{loadedImage}, img.RepoTags)

	t.Logf("create a container with the loaded image")
	sbConfig := PodSandboxConfig("sandbox", Randomize("image-load"))
	sb, err := runtimeService.RunPodSandbox(sbConfig, *runtimeHandler)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.StopPodSandbox(sb))
		assert.NoError(t, runtimeService.RemovePodSandbox(sb))
	}()
	containerConfig := ContainerConfig(
		"container",
		testImage,
		WithCommand("tail", "-f", "/dev/null"),
	)
	// Rely on sandbox clean to do container cleanup.
	cn, err := runtimeService.CreateContainer(sb, containerConfig, sbConfig)
	require.NoError(t, err)
	require.NoError(t, runtimeService.StartContainer(cn))

	t.Logf("make sure container is running")
	status, err := runtimeService.ContainerStatus(cn)
	require.NoError(t, err)
	require.Equal(t, runtime.ContainerState_CONTAINER_RUNNING, status.State)
}
