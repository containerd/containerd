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
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/client"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// Test to load an image from tarball.
func TestImageLoad(t *testing.T) {
	// TODO(kiashok): Docker is not able to pull the right
	// image manifest of `testImage` on WS2025 host. Temporarily
	// skipping this test for WS2025 while its fixed on docker.
	// This test is validated on WS2022 anyway.
	if goruntime.GOOS == "windows" && client.SkipTestOnHost() {
		t.Skip("Temporarily skip validating on WS2025")
	}

	testImage := images.Get(images.BusyBox)
	loadedImage := testImage
	_, err := exec.LookPath("docker")
	if err != nil {
		t.Skipf("Docker is not available: %v", err)
	}
	t.Logf("docker save image into tarball")
	output, err := exec.Command("docker", "pull", testImage).CombinedOutput()
	require.NoError(t, err, "output: %q", output)
	// os.CreateTemp also opens a file, which might prevent us from overwriting that file with docker save.
	tarDir := t.TempDir()
	tar := filepath.Join(tarDir, "image.tar")
	require.NoError(t, err)
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
	require.NoError(t, err, "ctr should be installed, make sure you've run `make install-deps`")
	// Add --local=true option since currently the transfer service
	// does not provide enough progress to avoid timeout
	output, err = exec.Command(ctr, "-address="+containerdEndpoint,
		"-n=k8s.io", "images", "import", "--local=true", tar).CombinedOutput()
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
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", Randomize("image-load"))
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
