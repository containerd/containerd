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
	"testing"
	"time"

	"github.com/containerd/containerd/pkg/failpoint"
	"github.com/containerd/continuity"
	"github.com/containerd/go-cni"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

const (
	failpointRuntimeHandler = "runc-fp"

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

// failpointConf is used to describe cmdAdd/cmdDel/cmdCheck command's failpoint.
type failpointConf struct {
	Add   string `json:"cmdAdd"`
	Del   string `json:"cmdDel"`
	Check string `json:"cmdCheck"`
}

func injectCNIFailpoint(t *testing.T, sbConfig *runtime.PodSandboxConfig, conf *failpointConf) {
	stateDir := t.TempDir()

	metadata := sbConfig.Metadata
	fpFilename := filepath.Join(stateDir,
		fmt.Sprintf("%s-%s.json", metadata.Namespace, metadata.Name))

	data, err := json.Marshal(conf)
	require.NoError(t, err)

	err = os.WriteFile(fpFilename, data, 0666)
	require.NoError(t, err)

	sbConfig.Annotations[failpointCNIConfPathKey] = fpFilename
}

func injectShimFailpoint(t *testing.T, sbConfig *runtime.PodSandboxConfig, methodFps map[string]string) {
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

	resp, err := client.Status(context.Background(), &runtime.StatusRequest{Verbose: true})
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
