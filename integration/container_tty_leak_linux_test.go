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
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
	remoteclient "k8s.io/client-go/tools/remotecommand"
	criruntime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestContainerTTYLeakAfterExit(t *testing.T) {
	t.Log("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "container-tty-leak-after-exit")

	testImage := images.Get(images.BusyBox)
	EnsureImageExists(t, testImage)

	var testcases = []struct {
		name  string
		stdin bool
	}{
		{
			name:  "ttyOnly",
			stdin: false,
		},
		{
			name:  "interactive",
			stdin: true,
		},
	}

	for _, testcase := range testcases {
		t.Run(testcase.name, func(t *testing.T) {
			t.Log("Create a container")
			cnConfig := ContainerConfig(
				testcase.name,
				testImage,
				WithCommand("sh", "-c", "sleep 365d"),
			)
			cnConfig.Stdin = testcase.stdin
			cnConfig.Tty = true

			t.Log("Create the container")
			cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
			require.NoError(t, err)

			t.Log("Start the container")
			require.NoError(t, runtimeService.StartContainer(cn))

			pid := getShimPid(t, sb)
			checkTTY(t, pid, 1)

			t.Log("Exec in container")
			rsp, err := runtimeService.Exec(&criruntime.ExecRequest{
				ContainerId: cn,
				Cmd:         []string{"sh", "-c", "echo tty"},
				Stderr:      false,
				Stdout:      true,
				Stdin:       testcase.stdin,
				Tty:         true,
			})
			require.NoError(t, err)

			execURL := rsp.Url
			URL, err := url.Parse(execURL)
			require.NoError(t, err)

			executor, err := remoteclient.NewSPDYExecutor(&rest.Config{}, "POST", URL)
			require.NoError(t, err)

			outBuf := bytes.NewBuffer(make([]byte, 64))
			streamOptions := remoteclient.StreamOptions{
				Stdout: outBuf,
				Tty:    true,
			}
			if testcase.stdin {
				streamOptions.Stdin = bytes.NewBuffer(nil)
			}

			require.NoError(t, executor.StreamWithContext(context.Background(), streamOptions))
			checkTTY(t, pid, 1)

			t.Log("Stop the container")
			require.NoError(t, runtimeService.StopContainer(cn, 10))

			t.Log("Remove the container")
			require.NoError(t, runtimeService.RemoveContainer(cn))

			checkTTY(t, pid, 0)
		})
	}

}

func getShimPid(t *testing.T, sb string) int {
	ctx := namespaces.WithNamespace(context.Background(), "k8s.io")
	shimCli := connectToShim(ctx, t, containerdEndpoint, 3, sb)
	return int(shimPid(ctx, t, shimCli))
}

func numTTY(shimPid int) int {
	cmd := exec.Command("sh", "-c", fmt.Sprintf("lsof -p %d | grep ptmx", shimPid))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return 0
	}
	return strings.Count(stdout.String(), "\n")
}

func checkTTY(t *testing.T, shimPid, expected int) {
	require.NoError(t, Eventually(func() (bool, error) {
		if numTTY(shimPid) == expected {
			return true, nil
		}
		return false, nil
	}, time.Second, 30*time.Second))
}
