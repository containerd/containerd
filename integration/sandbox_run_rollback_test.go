//go:build linux
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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	criapiv1 "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/pkg/failpoint"
)

const (
	failpointRuntimeHandler = "runc-fp"

	failpointShimPrefixKey = "io.containerd.runtime.v2.shim.failpoint."

	failpointCNIConfPathKey = "failpoint.cni.containerd.io/confpath"
)

func TestRunPodSandboxWithSetupCNIFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip()
	}

	t.Logf("Inject CNI failpoint")
	conf := &failpointConf{
		Add: "1*error(you-shall-not-pass!)",
	}

	sbConfig := PodSandboxConfig(t.Name(), "failpoint")
	injectCNIFailpoint(t, sbConfig, conf)

	t.Logf("Create a sandbox")
	_, err := runtimeService.RunPodSandbox(sbConfig, failpointRuntimeHandler)
	require.Error(t, err)
	require.Equal(t, true, strings.Contains(err.Error(), "you-shall-not-pass!"))

	t.Logf("Retry to create sandbox with same config")
	sb, err := runtimeService.RunPodSandbox(sbConfig, failpointRuntimeHandler)
	require.NoError(t, err)

	err = runtimeService.StopPodSandbox(sb)
	require.NoError(t, err)

	err = runtimeService.RemovePodSandbox(sb)
	require.NoError(t, err)
}

func TestRunPodSandboxWithShimStartFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip()
	}

	t.Logf("Inject Shim failpoint")

	sbConfig := PodSandboxConfig(t.Name(), "failpoint")
	injectShimFailpoint(t, sbConfig, map[string]string{
		"Start": "1*error(no hard feelings)",
	})

	t.Logf("Create a sandbox")
	_, err := runtimeService.RunPodSandbox(sbConfig, failpointRuntimeHandler)
	require.Error(t, err)
	require.Equal(t, true, strings.Contains(err.Error(), "no hard feelings"))
}

// TestRunPodSandboxWithShimDeleteFailure should keep the sandbox record if
// failed to rollback shim by shim.Delete API.
func TestRunPodSandboxWithShimDeleteFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip()
	}
	if os.Getenv("ENABLE_CRI_SANDBOXES") != "" {
		t.Skip()
	}

	testCase := func(restart bool) func(*testing.T) {
		return func(t *testing.T) {
			t.Log("Init PodSandboxConfig with specific label")
			labels := map[string]string{
				t.Name(): "true",
			}
			sbConfig := PodSandboxConfig(t.Name(), "failpoint", WithPodLabels(labels))

			t.Log("Inject Shim failpoint")
			injectShimFailpoint(t, sbConfig, map[string]string{
				"Start":  "1*error(failed to start shim)",
				"Delete": "1*error(please retry)", // inject failpoint during rollback shim
			})

			t.Log("Create a sandbox")
			_, err := runtimeService.RunPodSandbox(sbConfig, failpointRuntimeHandler)
			require.Error(t, err)
			require.ErrorContains(t, err, "failed to start shim")

			t.Log("ListPodSandbox with the specific label")
			l, err := runtimeService.ListPodSandbox(&criapiv1.PodSandboxFilter{LabelSelector: labels})
			require.NoError(t, err)
			require.Len(t, l, 1)

			sb := l[0]
			require.Equal(t, sb.State, criapiv1.PodSandboxState_SANDBOX_NOTREADY)
			require.Equal(t, sb.Metadata.Name, sbConfig.Metadata.Name)
			require.Equal(t, sb.Metadata.Namespace, sbConfig.Metadata.Namespace)
			require.Equal(t, sb.Metadata.Uid, sbConfig.Metadata.Uid)
			require.Equal(t, sb.Metadata.Attempt, sbConfig.Metadata.Attempt)

			t.Log("Check PodSandboxStatus")
			sbStatus, err := runtimeService.PodSandboxStatus(sb.Id)
			require.NoError(t, err)
			require.Equal(t, sbStatus.State, criapiv1.PodSandboxState_SANDBOX_NOTREADY)
			require.Greater(t, len(sbStatus.Network.Ip), 0)

			if restart {
				t.Log("Restart containerd")
				RestartContainerd(t)

				t.Log("ListPodSandbox with the specific label")
				l, err = runtimeService.ListPodSandbox(&criapiv1.PodSandboxFilter{Id: sb.Id})
				require.NoError(t, err)
				require.Len(t, l, 1)
				require.Equal(t, l[0].State, criapiv1.PodSandboxState_SANDBOX_NOTREADY)

				t.Log("Check PodSandboxStatus")
				sbStatus, err := runtimeService.PodSandboxStatus(sb.Id)
				require.NoError(t, err)
				t.Log(sbStatus.Network)
				require.Equal(t, sbStatus.State, criapiv1.PodSandboxState_SANDBOX_NOTREADY)
			}

			t.Log("Cleanup leaky sandbox")
			err = runtimeService.RemovePodSandbox(sb.Id)
			require.NoError(t, err)
		}
	}

	t.Run("CleanupAfterRestart", testCase(true))
	t.Run("JustCleanup", testCase(false))
}

// TestRunPodSandboxWithShimStartAndTeardownCNIFailure should keep the sandbox
// record if failed to rollback CNI API.
func TestRunPodSandboxWithShimStartAndTeardownCNIFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip()
	}
	if os.Getenv("ENABLE_CRI_SANDBOXES") != "" {
		t.Skip()
	}

	testCase := func(restart bool) func(*testing.T) {
		return func(t *testing.T) {
			t.Log("Init PodSandboxConfig with specific key")
			labels := map[string]string{
				t.Name(): "true",
			}
			sbConfig := PodSandboxConfig(t.Name(), "failpoint", WithPodLabels(labels))

			t.Log("Inject Shim failpoint")
			injectShimFailpoint(t, sbConfig, map[string]string{
				"Start": "1*error(failed to start shim)",
			})

			t.Log("Inject CNI failpoint")
			conf := &failpointConf{
				Del: "1*error(please retry)",
			}
			injectCNIFailpoint(t, sbConfig, conf)

			t.Log("Create a sandbox")
			_, err := runtimeService.RunPodSandbox(sbConfig, failpointRuntimeHandler)
			require.Error(t, err)
			require.ErrorContains(t, err, "failed to start shim")

			t.Log("ListPodSandbox with the specific label")
			l, err := runtimeService.ListPodSandbox(&criapiv1.PodSandboxFilter{LabelSelector: labels})
			require.NoError(t, err)
			require.Len(t, l, 1)

			sb := l[0]
			require.Equal(t, sb.State, criapiv1.PodSandboxState_SANDBOX_NOTREADY)
			require.Equal(t, sb.Metadata.Name, sbConfig.Metadata.Name)
			require.Equal(t, sb.Metadata.Namespace, sbConfig.Metadata.Namespace)
			require.Equal(t, sb.Metadata.Uid, sbConfig.Metadata.Uid)
			require.Equal(t, sb.Metadata.Attempt, sbConfig.Metadata.Attempt)

			if restart {
				t.Log("Restart containerd")
				RestartContainerd(t)

				t.Log("ListPodSandbox with the specific label")
				l, err = runtimeService.ListPodSandbox(&criapiv1.PodSandboxFilter{Id: sb.Id})
				require.NoError(t, err)
				require.Len(t, l, 1)
				require.Equal(t, l[0].State, criapiv1.PodSandboxState_SANDBOX_NOTREADY)
			}

			t.Log("Cleanup leaky sandbox")
			err = runtimeService.RemovePodSandbox(sb.Id)
			require.NoError(t, err)
		}
	}
	t.Run("CleanupAfterRestart", testCase(true))
	t.Run("JustCleanup", testCase(false))
}

// failpointConf is used to describe cmdAdd/cmdDel/cmdCheck command's failpoint.
type failpointConf struct {
	Add   string `json:"cmdAdd"`
	Del   string `json:"cmdDel"`
	Check string `json:"cmdCheck"`
}

func injectCNIFailpoint(t *testing.T, sbConfig *criapiv1.PodSandboxConfig, conf *failpointConf) {
	stateDir := t.TempDir()

	metadata := sbConfig.Metadata
	fpFilename := filepath.Join(stateDir,
		fmt.Sprintf("%s-%s.json", metadata.Namespace, strings.Replace(metadata.Name, "/", "-", -1)))

	data, err := json.Marshal(conf)
	require.NoError(t, err)

	err = os.WriteFile(fpFilename, data, 0666)
	require.NoError(t, err)

	sbConfig.Annotations[failpointCNIConfPathKey] = fpFilename
}

func injectShimFailpoint(t *testing.T, sbConfig *criapiv1.PodSandboxConfig, methodFps map[string]string) {
	for method, fp := range methodFps {
		_, err := failpoint.NewFailpoint(method, fp)
		require.NoError(t, err, "check failpoint %s for shim method %s", fp, method)

		sbConfig.Annotations[failpointShimPrefixKey+method] = fp
	}
}
