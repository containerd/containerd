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
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/require"
)

func TestRuncLeakWithShimKilled(t *testing.T) {
	testImage := images.Get(images.BusyBox)
	cnConfig := ContainerConfig(
		"test-container-dir-leak",
		testImage,
		WithCommand("sh", "-c", "sleep 365d"),
	)
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "test-container-dir-leak-after-shimkilled")
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)
	t.Logf("Start the container %s", cn)
	require.NoError(t, runtimeService.StartContainer(cn))
	dir := fmt.Sprintf("/run/containerd/runc/k8s.io/%s", cn)
	_, err = os.Stat(dir)
	require.NoError(t, err)
	pid := getShimPid(t, sb)
	KillPid(pid)
	runtimeService.RemoveContainer(cn)
	// wait the leak dir to be deleted.
	result := make(chan error)
	go func() {
		for {
			time.Sleep(time.Millisecond * 100)
			_, err = os.Stat(dir)
			if err == nil {
				continue
			}
			result <- err
			break
		}
	}()
	select {
	case <-time.After(time.Second * 30):
		t.Fatalf("dir %s should be deleted", dir)
	case res := <-result:
		if !errors.Is(res, os.ErrNotExist) {
			t.Fatalf("err should be %s but got %s", os.ErrNotExist, res.Error())
		}
	}
	//wait the task to be removed
	time.Sleep(time.Second * 3)
}
