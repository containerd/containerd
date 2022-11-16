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
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/pkg/cri/store/sandbox"
	"github.com/containerd/containerd/pkg/failpoint"
	"github.com/containerd/continuity"
	"github.com/containerd/go-cni"
	"github.com/containerd/typeurl"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	criapiv1alpha2 "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

const (
	failpointRuntimeHandler = "runc-fp"
	failpointCNIBinary      = "cni-bridge-fp"

	failpointShimPrefixKey = "io.containerd.runtime.v2.shim.failpoint."

	failpointCNIConfPathKey = "failpoint.cni.containerd.io/confpath"
)

func TestRunPodSandboxWithSetupCNIFailure(t *testing.T) {
	defer prepareFailpointCNI(t)()

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
			require.Contains(t, err.Error(), "failed to start shim")

			t.Log("ListPodSandbox with the specific label")
			l, err := runtimeService.ListPodSandbox(&criapiv1alpha2.PodSandboxFilter{LabelSelector: labels})
			require.NoError(t, err)
			require.Len(t, l, 1)

			sb := l[0]
			require.Equal(t, sb.State, criapiv1alpha2.PodSandboxState_SANDBOX_NOTREADY)
			require.Equal(t, sb.Metadata.Name, sbConfig.Metadata.Name)
			require.Equal(t, sb.Metadata.Namespace, sbConfig.Metadata.Namespace)
			require.Equal(t, sb.Metadata.Uid, sbConfig.Metadata.Uid)
			require.Equal(t, sb.Metadata.Attempt, sbConfig.Metadata.Attempt)

			t.Log("Check PodSandboxStatus")
			sbStatus, err := runtimeService.PodSandboxStatus(sb.Id)
			require.NoError(t, err)
			require.Equal(t, sbStatus.State, criapiv1alpha2.PodSandboxState_SANDBOX_NOTREADY)
			require.Greater(t, len(sbStatus.Network.Ip), 0)

			if restart {
				t.Log("Restart containerd")
				RestartContainerd(t, syscall.SIGTERM)

				t.Log("ListPodSandbox with the specific label")
				l, err = runtimeService.ListPodSandbox(&criapiv1alpha2.PodSandboxFilter{Id: sb.Id})
				require.NoError(t, err)
				require.Len(t, l, 1)
				require.Equal(t, l[0].State, criapiv1alpha2.PodSandboxState_SANDBOX_NOTREADY)

				t.Log("Check PodSandboxStatus")
				sbStatus, err := runtimeService.PodSandboxStatus(sb.Id)
				require.NoError(t, err)
				t.Log(sbStatus.Network)
				require.Equal(t, sbStatus.State, criapiv1alpha2.PodSandboxState_SANDBOX_NOTREADY)
			}

			t.Log("Cleanup leaky sandbox")
			err = runtimeService.StopPodSandbox(sb.Id)
			require.NoError(t, err)
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
	testCase := func(restart bool) func(*testing.T) {
		return func(t *testing.T) {
			defer prepareFailpointCNI(t)()

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
			require.Contains(t, err.Error(), "failed to start shim")

			t.Log("ListPodSandbox with the specific label")
			l, err := runtimeService.ListPodSandbox(&criapiv1alpha2.PodSandboxFilter{LabelSelector: labels})
			require.NoError(t, err)
			require.Len(t, l, 1)

			sb := l[0]
			require.Equal(t, sb.State, criapiv1alpha2.PodSandboxState_SANDBOX_NOTREADY)
			require.Equal(t, sb.Metadata.Name, sbConfig.Metadata.Name)
			require.Equal(t, sb.Metadata.Namespace, sbConfig.Metadata.Namespace)
			require.Equal(t, sb.Metadata.Uid, sbConfig.Metadata.Uid)
			require.Equal(t, sb.Metadata.Attempt, sbConfig.Metadata.Attempt)

			if restart {
				t.Log("Restart containerd")
				RestartContainerd(t, syscall.SIGTERM)

				t.Log("ListPodSandbox with the specific label")
				l, err = runtimeService.ListPodSandbox(&criapiv1alpha2.PodSandboxFilter{Id: sb.Id})
				require.NoError(t, err)
				require.Len(t, l, 1)
				require.Equal(t, l[0].State, criapiv1alpha2.PodSandboxState_SANDBOX_NOTREADY)
			}

			t.Log("Cleanup leaky sandbox")
			err = runtimeService.StopPodSandbox(sb.Id)
			require.NoError(t, err)
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
	defer prepareFailpointCNI(t)()

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
		require.Contains(t, err.Error(), "transport is closing")
	}()

	assert.NoError(t, ensureCNIAddRunning(t, sbName), "check that failpoint CNI.Add is running")

	// Use SIGKILL to prevent containerd server gracefulshutdown which may cause indeterministic invocation of defer functions
	t.Log("Restart containerd")
	RestartContainerd(t, syscall.SIGKILL)

	wg.Wait()

	t.Log("ListPodSandbox with the specific label")
	l, err := runtimeService.ListPodSandbox(&criapiv1alpha2.PodSandboxFilter{LabelSelector: labels})
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

	assert.Equal(t, sb.State, criapiv1alpha2.PodSandboxState_SANDBOX_NOTREADY)
	assert.Equal(t, sb.Metadata.Name, sbConfig.Metadata.Name)
	assert.Equal(t, sb.Metadata.Namespace, sbConfig.Metadata.Namespace)
	assert.Equal(t, sb.Metadata.Uid, sbConfig.Metadata.Uid)
	assert.Equal(t, sb.Metadata.Attempt, sbConfig.Metadata.Attempt)

	t.Log("Get sandbox info")
	_, info, err := SandboxInfo(sb.Id)
	require.NoError(t, err)
	require.False(t, info.NetNSClosed)

	var netNS string
	for _, n := range info.RuntimeSpec.Linux.Namespaces {
		if n.Type == runtimespec.NetworkNamespace {
			netNS = n.Path
		}
	}
	assert.NotEmpty(t, netNS, "network namespace should be set")

	t.Log("Get sandbox container")
	c, err := GetContainer(sb.Id)
	require.NoError(t, err)
	any, ok := c.Extensions["io.cri-containerd.sandbox.metadata"]
	require.True(t, ok, "sandbox metadata should exist in extension")
	i, err := typeurl.UnmarshalAny(&any)
	require.NoError(t, err)
	require.IsType(t, &sandbox.Metadata{}, i)
	metadata, ok := i.(*sandbox.Metadata)
	require.True(t, ok)
	assert.NotEmpty(t, metadata.NetNSPath)
	assert.Equal(t, netNS, metadata.NetNSPath, "network namespace path should be the same in runtime spec and sandbox metadata")
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
				kv := strings.SplitN(arg, "=", 2)
				if len(kv) != 2 {
					continue
				}

				if kv[0] == "K8S_POD_NAME" && kv[1] == sbName {
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

func injectCNIFailpoint(t *testing.T, sbConfig *criapiv1alpha2.PodSandboxConfig, conf *failpointConf) {
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

func injectShimFailpoint(t *testing.T, sbConfig *criapiv1alpha2.PodSandboxConfig, methodFps map[string]string) {
	for method, fp := range methodFps {
		_, err := failpoint.NewFailpoint(method, fp)
		require.NoError(t, err, "check failpoint %s for shim method %s", fp, method)

		sbConfig.Annotations[failpointShimPrefixKey+method] = fp
	}
}

var (
	cniConfFilename      = "10-containerd-net.conflist"
	cniFailpointConfName = "containerd-net-failpoint"
	cniFailpointConfData = `
{
  "cniVersion": "0.4.0",
  "name": "containerd-net-failpoint",
  "plugins": [
    {
      "type": "cni-bridge-fp",
      "bridge": "cni-fp",
      "isGateway": true,
      "ipMasq": true,
      "promiscMode": true,
      "ipam": {
        "type": "host-local",
        "ranges": [
          [{
            "subnet": "10.88.0.0/16"
          }],
          [{
            "subnet": "2001:4860:4860::/64"
          }]
        ],
        "routes": [
          { "dst": "0.0.0.0/0" },
          { "dst": "::/0" }
        ]
      },
      "capabilities": {
        "io.kubernetes.cri.pod-annotations": true
      }
    },
    {
      "type": "portmap",
      "capabilities": {"portMappings": true}
    }
  ]
}
`
)

// prepareFailpointCNI replaces the existing CNI config and returns reset function.
func prepareFailpointCNI(t *testing.T) (_rollback func()) {
	t.Logf("Preparing Failpoint CNI config: %v", cniFailpointConfName)

	cfg, err := getCNIConfig()
	require.NoError(t, err, "failed to get cni config")

	cniConfPath := filepath.Join(cfg.PluginConfDir, cniConfFilename)

	oConfData, err := ioutil.ReadFile(cniConfPath)
	require.NoError(t, err, "failed to read old cni config")
	oConfName := cfg.Networks[1].Config.Name

	t.Logf("Original CNI Config: %v", string(oConfData))

	err = continuity.AtomicWriteFile(cniConfPath, []byte(cniFailpointConfData), 0600)
	require.NoError(t, err, "failed to override %v", cniConfPath)

	require.NoError(t, Eventually(func() (bool, error) {
		cniCfg, err := getCNIConfig()
		if err != nil {
			t.Logf("failed to get ready CNI config: %v, retrying", err)
			return false, nil
		}

		return cniCfg.Networks[1].Config.Name == cniFailpointConfName, nil
	}, time.Second, 30*time.Second), "Wait for CNI to be ready")

	return func() {
		t.Logf("Resetting CNI config")

		err = continuity.AtomicWriteFile(cniConfPath, oConfData, 0600)
		require.NoError(t, err, "failed to override %v", cniConfPath)

		require.NoError(t, Eventually(func() (bool, error) {
			cniCfg, err := getCNIConfig()
			if err != nil {
				t.Logf("failed to get ready CNI config: %v, retrying", err)
				return false, nil
			}

			return cniCfg.Networks[1].Config.Name == oConfName, nil
		}, time.Second, 30*time.Second), "Wait for CNI to be ready")
	}
}

func getCNIConfig() (*cni.ConfigResult, error) {
	client, err := RawRuntimeClient()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get raw runtime client")
	}

	resp, err := client.Status(context.Background(), &criapiv1alpha2.StatusRequest{Verbose: true})
	if err != nil {
		return nil, errors.Wrap(err, "failed to get status")
	}

	if status := resp.Info["lastCNILoadStatus"]; status != "OK" {
		return nil, errors.Errorf("containerd reports that CNI is not ready: %v", status)
	}

	config := &cni.ConfigResult{}
	if err := json.Unmarshal([]byte(resp.Info["cniconfig"]), config); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal config")
	}
	return config, nil
}
