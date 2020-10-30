// +build linux

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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

func TestNonRootUserCap(t *testing.T) {
	for name, test := range map[string]struct {
		uid   int64
		runOK bool
	}{
		"shouldn't be able to run test container, with a private workdir that is accessible to nobody, as non-root user": {
			uid:   1234,
			runOK: false,
		},
		"should be able to run test container, with a private workdir that is accessible to nobody, as root user": {
			uid:   0,
			runOK: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Log("Create a sandbox")
			sbConfig := PodSandboxConfig("sandbox", "non-root-user-cap")
			sb, err := runtimeService.RunPodSandbox(sbConfig, *runtimeHandler)
			require.NoError(t, err)
			// Make sure the sandbox is cleaned up.
			defer func() {
				assert.NoError(t, runtimeService.StopPodSandbox(sb))
				assert.NoError(t, runtimeService.RemovePodSandbox(sb))
			}()

			const (
				testImage     = "gcr.io/k8s-cri-containerd/private-workdir:1.0"
				containerName = "test-container"
			)
			t.Logf("Pull test image %q", testImage)
			img, err := imageService.PullImage(&runtime.ImageSpec{Image: testImage}, nil, sbConfig)
			require.NoError(t, err)
			defer func() {
				assert.NoError(t, imageService.RemoveImage(&runtime.ImageSpec{Image: img}))
			}()

			t.Log("Create a container to test capabilities")
			cnConfig := ContainerConfig(
				containerName,
				testImage,
				WithCommand("ls", "."),
				WithLogPath(containerName),
				WithRunAsUser(test.uid),
			)
			cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
			require.NoError(t, err)

			// TODO: make this idiomatic versus the other integration tests
			t.Log("Start the container")
			err = runtimeService.StartContainer(cn)
			if test.runOK && err != nil {
				require.NoError(t, err)
			}

			t.Log("Wait for container exit")
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

			s, err := runtimeService.ContainerStatus(cn)
			require.NoError(t, err)
			if test.runOK {
				if s.ExitCode != 0 {
					t.Fatal("Expected success but non zero exit")
				}
			} else {
				if s.ExitCode == 0 {
					t.Fatal("Expected failure but exited zero")
				}
			}
		})
	}
}
