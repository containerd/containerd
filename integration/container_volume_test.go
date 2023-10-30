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
	goruntime "runtime"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func createRegularFile(basePath, content string) (string, error) {
	newFolder := filepath.Join(basePath, "regular")
	err := os.Mkdir(newFolder, 0755)
	if err != nil {
		return "", err
	}

	newFile := filepath.Join(newFolder, "foo.txt")
	err = os.WriteFile(newFile, []byte(content), 0644)
	return filepath.Join("regular", "foo.txt"), err
}

func fileInSymlinkedFolder(basePath, targetFile string) (string, error) {
	symlinkFolder := filepath.Join(basePath, "symlink_folder")
	err := os.Symlink(filepath.Dir(targetFile), symlinkFolder)

	return filepath.Join(symlinkFolder, filepath.Base(targetFile)), err
}

func symlinkedFile(basePath, targetFile string) (string, error) {
	symlinkFile := filepath.Join(basePath, "symlink_file")
	err := os.Symlink(targetFile, symlinkFile)

	return symlinkFile, err
}

func symlinkedFileInSymlinkedFolder(basePath, targetFile string) (string, error) {
	symlinkFolderFile, err := fileInSymlinkedFolder(basePath, targetFile)
	if err != nil {
		return "", err
	}

	return symlinkedFile(basePath, symlinkFolderFile)
}

func TestContainerSymlinkVolumes(t *testing.T) {
	for name, testCase := range map[string]struct {
		createFileFn func(basePath, targetFile string) (string, error)
	}{
		// Create difference file / symlink scenarios:
		// - symlink_file -> regular_folder/regular_file
		// - symlink_folder/regular_file (symlink_folder -> regular_folder)
		// - symlink_file -> symlink_folder/regular_file (symlink_folder -> regular_folder)
		"file in symlinked folder": {
			createFileFn: fileInSymlinkedFolder,
		},
		"symlinked file": {
			createFileFn: symlinkedFile,
		},
		"symlinkedFileInSymlinkedFolder": {
			createFileFn: symlinkedFileInSymlinkedFolder,
		},
	} {
		testCase := testCase // capture range variable
		t.Run(name, func(t *testing.T) {
			testPodLogDir := t.TempDir()
			testVolDir := t.TempDir()

			content := "hello there\n"
			regularFile, err := createRegularFile(testVolDir, content)
			require.NoError(t, err)

			file, err := testCase.createFileFn(testVolDir, regularFile)
			require.NoError(t, err)

			t.Log("Create test sandbox with log directory")
			sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "test-symlink",
				WithPodLogDirectory(testPodLogDir),
			)

			var (
				testImage          = images.Get(images.BusyBox)
				containerName      = "test-container"
				containerMountPath = "/mounted_file"
			)

			if goruntime.GOOS == "windows" {
				containerMountPath = filepath.Clean("C:" + containerMountPath)
			}

			EnsureImageExists(t, testImage)

			t.Log("Create a container with a symlink volume mount")
			cnConfig := ContainerConfig(
				containerName,
				testImage,
				WithCommand("cat", containerMountPath),
				WithLogPath(containerName),
				WithVolumeMount(file, containerMountPath),
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

			output, err := os.ReadFile(filepath.Join(testPodLogDir, containerName))
			assert.NoError(t, err)

			assert.Contains(t, string(output), content)
		})
	}
}
