//go:build linux

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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	criapiv1 "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/internal/cri/types"
	"github.com/containerd/containerd/v2/internal/failpoint"
)

const (
	failpointRuntimeHandler = "runc-fp"
	failpointCNIBinary      = "cni-bridge-fp"

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
	require.ErrorContains(t, err, "you-shall-not-pass!")

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
	require.ErrorContains(t, err, "no hard feelings")
}

// TestRunPodSandboxWithShimDeleteFailure should keep the sandbox record if
// failed to rollback shim by shim.Delete API.
func TestRunPodSandboxWithShimDeleteFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
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
			require.Equal(t, criapiv1.PodSandboxState_SANDBOX_NOTREADY, sb.State)
			require.Equal(t, sbConfig.Metadata.Name, sb.Metadata.Name)
			require.Equal(t, sbConfig.Metadata.Namespace, sb.Metadata.Namespace)
			require.Equal(t, sbConfig.Metadata.Uid, sb.Metadata.Uid)
			require.Equal(t, sbConfig.Metadata.Attempt, sb.Metadata.Attempt)

			t.Log("Check PodSandboxStatus")
			sbStatus, err := runtimeService.PodSandboxStatus(sb.Id)
			require.NoError(t, err)
			require.Equal(t, criapiv1.PodSandboxState_SANDBOX_NOTREADY, sbStatus.State)
			require.Greater(t, len(sbStatus.Network.Ip), 0)

			if restart {
				t.Log("Restart containerd")
				RestartContainerd(t, syscall.SIGTERM)

				t.Log("ListPodSandbox with the specific label")
				l, err = runtimeService.ListPodSandbox(&criapiv1.PodSandboxFilter{Id: sb.Id})
				require.NoError(t, err)
				require.Len(t, l, 1)
				require.Equal(t, criapiv1.PodSandboxState_SANDBOX_NOTREADY, l[0].State)

				t.Log("Check PodSandboxStatus")
				sbStatus, err := runtimeService.PodSandboxStatus(sb.Id)
				require.NoError(t, err)
				t.Log(sbStatus.Network)
				require.Equal(t, criapiv1.PodSandboxState_SANDBOX_NOTREADY, sbStatus.State)
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
			require.Equal(t, criapiv1.PodSandboxState_SANDBOX_NOTREADY, sb.State)
			require.Equal(t, sbConfig.Metadata.Name, sb.Metadata.Name)
			require.Equal(t, sbConfig.Metadata.Namespace, sb.Metadata.Namespace)
			require.Equal(t, sbConfig.Metadata.Uid, sb.Metadata.Uid)
			require.Equal(t, sbConfig.Metadata.Attempt, sb.Metadata.Attempt)

			if restart {
				t.Log("Restart containerd")
				RestartContainerd(t, syscall.SIGTERM)

				t.Log("ListPodSandbox with the specific label")
				l, err = runtimeService.ListPodSandbox(&criapiv1.PodSandboxFilter{Id: sb.Id})
				require.NoError(t, err)
				require.Len(t, l, 1)
				require.Equal(t, criapiv1.PodSandboxState_SANDBOX_NOTREADY, l[0].State)
			}

			t.Log("Cleanup leaky sandbox")
			err = runtimeService.RemovePodSandbox(sb.Id)
			require.NoError(t, err)
		}
	}
	t.Run("CleanupAfterRestart", testCase(true))
	t.Run("JustCleanup", testCase(false))
}

// TestRunPodSandboxWithShimStartAndTeardownCNISlow should keep the sandbox
// record if failed to rollback CNI API.
func TestRunPodSandboxAndTeardownCNISlow(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip()
	}

	t.Log("Init PodSandboxConfig with specific key")
	sbName := t.Name()
	labels := map[string]string{
		sbName: "true",
	}
	sbConfig := PodSandboxConfig(sbName, "failpoint", WithPodLabels(labels))

	t.Log("Inject CNI failpoint")
	conf := &failpointConf{
		// Delay 1 day
		Add: "1*delay(86400000)",
	}
	injectCNIFailpoint(t, sbConfig, conf)

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		t.Log("Create a sandbox")
		_, err := runtimeService.RunPodSandbox(sbConfig, failpointRuntimeHandler)
		require.Error(t, err)
		require.ErrorContains(t, err, "error reading from server: EOF")
	}()

	assert.NoError(t, ensureCNIAddRunning(t, sbName), "check that failpoint CNI.Add is running")

	// Use SIGKILL to prevent containerd server gracefulshutdown which may cause indeterministic invocation of defer functions
	t.Log("Restart containerd")
	RestartContainerd(t, syscall.SIGKILL)

	wg.Wait()

	t.Log("ListPodSandbox with the specific label")
	l, err := runtimeService.ListPodSandbox(&criapiv1.PodSandboxFilter{LabelSelector: labels})
	require.NoError(t, err)
	require.Len(t, l, 1)

	sb := l[0]

	defer func() {
		t.Log("Cleanup leaky sandbox")
		err := runtimeService.StopPodSandbox(sb.Id)
		assert.NoError(t, err)
		err = runtimeService.RemovePodSandbox(sb.Id)
		require.NoError(t, err)
	}()

	assert.Equal(t, criapiv1.PodSandboxState_SANDBOX_NOTREADY, sb.State)
	assert.Equal(t, sbConfig.Metadata.Name, sb.Metadata.Name)
	assert.Equal(t, sbConfig.Metadata.Namespace, sb.Metadata.Namespace)
	assert.Equal(t, sbConfig.Metadata.Uid, sb.Metadata.Uid)
	assert.Equal(t, sbConfig.Metadata.Attempt, sb.Metadata.Attempt)

	t.Log("Get sandbox info (sbserver)")
	_, info, err := sbserverSandboxInfo(sb.Id)
	require.NoError(t, err)
	require.False(t, info.NetNSClosed)

	assert.NotEmpty(t, info.Metadata.NetNSPath, "network namespace should be set")
}

// sbserverSandboxInfo gets sandbox info.
func sbserverSandboxInfo(id string) (*criapiv1.PodSandboxStatus, *types.SandboxInfo, error) {
	client, err := RawRuntimeClient()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get raw runtime client: %w", err)
	}
	resp, err := client.PodSandboxStatus(context.Background(), &criapiv1.PodSandboxStatusRequest{
		PodSandboxId: id,
		Verbose:      true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get sandbox status: %w", err)
	}
	status := resp.GetStatus()
	var info types.SandboxInfo
	if err := json.Unmarshal([]byte(resp.GetInfo()["info"]), &info); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal sandbox info: %w", err)
	}
	return status, &info, nil
}

func ensureCNIAddRunning(t *testing.T, sbName string) error {
	return Eventually(func() (bool, error) {
		pids, err := PidsOf(failpointCNIBinary)
		if err != nil || len(pids) == 0 {
			return false, err
		}

		for _, pid := range pids {
			envs, err := PidEnvs(pid)
			if err != nil {
				t.Logf("failed to read environ of pid %v: %v: skip it", pid, err)
				continue
			}

			args, ok := envs["CNI_ARGS"]
			if !ok {
				t.Logf("expected CNI_ARGS env but got nothing, skip pid=%v", pid)
				continue
			}

			for _, arg := range strings.Split(args, ";") {
				if arg == "K8S_POD_NAME="+sbName {
					return true, nil
				}
			}
		}
		return false, nil
	}, time.Second, 30*time.Second)
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
