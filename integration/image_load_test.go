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
	"golang.org/x/net/context"
	"io/ioutil"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	api "github.com/kubernetes-incubator/cri-containerd/pkg/api/v1"
)

// Test to load an image from tarball.
func TestImageLoad(t *testing.T) {
	testImage := "busybox:latest"
	loadedImage := "docker.io/library/" + testImage
	_, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("Docker is not available: %v", err)
	}
	t.Logf("docker save image into tarball")
	output, err := exec.Command("docker", "pull", testImage).CombinedOutput()
	require.NoError(t, err, "output: %q", output)
	tarF, err := ioutil.TempFile("", "image-load")
	tar := tarF.Name()
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, os.RemoveAll(tar))
	}()
	output, err = exec.Command("docker", "save", testImage, "-o", tar).CombinedOutput()
	require.NoError(t, err, "output: %q", output)

	t.Logf("make sure no such image in cri-containerd")
	img, err := imageService.ImageStatus(&runtime.ImageSpec{Image: testImage})
	require.NoError(t, err)
	if img != nil {
		require.NoError(t, imageService.RemoveImage(&runtime.ImageSpec{Image: testImage}))
	}

	t.Logf("load image in cri-containerd")
	res, err := criContainerdClient.LoadImage(context.Background(), &api.LoadImageRequest{FilePath: tar})
	require.NoError(t, err)
	require.Equal(t, []string{loadedImage}, res.GetImages())

	t.Logf("make sure image is loaded")
	img, err = imageService.ImageStatus(&runtime.ImageSpec{Image: testImage})
	require.NoError(t, err)
	require.NotNil(t, img)
	require.Equal(t, []string{loadedImage}, img.RepoTags)

	t.Logf("create a container with the loaded image")
	sbConfig := PodSandboxConfig("sandbox", Randomize("image-load"))
	sb, err := runtimeService.RunPodSandbox(sbConfig)
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
