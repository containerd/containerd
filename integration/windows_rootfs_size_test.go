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
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestWindowsRootfsSize(t *testing.T) {
	testPodLogDir := t.TempDir()

	t.Log("Create a sandbox with log directory")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "windows-rootfs-size",
		WithPodLogDirectory(testPodLogDir),
	)

	var (
		testImage     = images.Get(images.Pause)
		containerName = "test-container"
	)

	EnsureImageExists(t, testImage)

	t.Log("Create a container to run the rootfs size test")

	// Ask for 200GiB disk size
	rootfsSize := int64(200 * 1024 * 1024 * 1024)
	cnConfig := ContainerConfig(
		containerName,
		testImage,
		// Execute dir on the root of the image as it'll show the size available.
		// We're asking for ten times the default volume size so this should be
		// easy to verify.
		WithCommand("cmd", "/c", "dir", "/-C", "C:\\"),
		WithLogPath(containerName),
		WithWindowsResources(&runtime.WindowsContainerResources{RootfsSizeInBytes: rootfsSize}),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))

	t.Log("Wait for container to finish running")
	require.NoError(t, Eventually(func() (bool, error) {
		s, err := runtimeService.ContainerStatus(cn)
		if err != nil {
			return false, err
		}
		if s.GetState() == runtime.ContainerState_CONTAINER_EXITED {
			return true, nil
		}
		return false, nil
	}, time.Second, 30*time.Second))

	t.Log("Check container log")
	content, err := os.ReadFile(filepath.Join(testPodLogDir, containerName))
	assert.NoError(t, err)

	// Format of output for dir /-C:
	//
	// Volume in drive C has no label.
	// Volume Serial Number is 5CA1-BDE0

	// Directory of C:\
	//
	// 05/05/2022  09:36 AM              5510 License.txt
	// 05/12/2022  08:34 PM    <DIR>          Users
	// 05/12/2022  08:34 PM    <DIR>          Windows
	// 			1 File(s)           5510 bytes
	// 			2 Dir(s)    214545743872 bytes free
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	found := false
	var (
		cols      []string
		driveSize int64
	)
	for scanner.Scan() {
		outputLine := scanner.Text()
		cols = strings.Fields(outputLine)
		n := len(cols)
		if n >= 3 {
			if cols[n-2] == "bytes" && cols[n-1] == "free" {
				driveSize, err = strconv.ParseInt(cols[n-3], 10, 64)
				if err != nil {
					t.Fatal(err)
				}
				found = true
				break
			}
		}
	}

	if !found {
		t.Log(string(content))
		t.Fatalf("could not find the size available on the drive")
	}

	// Compare the bytes available at the root of the drive with the 200GiB we asked for. They won't
	// match up exactly as space is always occupied but we're giving 300MiB of leeway for content on
	// the virtual drive.
	toleranceInMB := int64(300 * 1024 * 1024)
	if driveSize < (rootfsSize - toleranceInMB) {
		t.Log(string(content))
		t.Fatalf("Size of the C:\\ volume is not within 300MiB of 200GiB. It is %d bytes", driveSize)
	}
}
