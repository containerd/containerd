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
	"fmt"
	"os"
	"path"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestCompatibilityWith17(t *testing.T) {
	var (
		latestVersion = "v1.7.27"
		workDir       = t.TempDir()
		defaultrt     = "runc"
		v1rt          = "runcv1"
	)

	downloadReleaseBinary(t, workDir, latestVersion)
	t.Cleanup(func() {
		_ = os.RemoveAll(workDir)
	})
	t.Logf("Install config for release %s", latestVersion)
	ctrdcfg := newCtrdConfig(t, workDir, 2)
	ctrdcfg.setRuntime(runtimeConfig{
		name:        defaultrt,
		runtimeType: "io.containerd.runc.v2",
		runtimePath: filepath.Join(workDir, "bin", "containerd-shim-runc-v2"),
	}, runtimeConfig{
		name:        v1rt,
		runtimeType: "io.containerd.runc.v1",
		runtimePath: filepath.Join(workDir, "bin", "containerd-shim-runc-v1"),
	})

	t.Log("Starting the previous release's containerd")
	previousCtrdBinPath := filepath.Join(workDir, "bin", "containerd")
	previousProc := newCtrdProc(t, previousCtrdBinPath, workDir, []string{})
	require.NoError(t, previousProc.isReady())

	t.Cleanup(func() {
		if t.Failed() {
			dumpFileContent(t, previousProc.logPath())
		}
	})
	type sbcnt struct {
		sbconfig *runtimev1.PodSandboxConfig
		sbid     string
		cntid    []string
	}
	var (
		testImage = images.Get(images.BusyBox)
		cnConfig  = ContainerConfig(
			"test",
			testImage,
			WithCommand("sh", "-c", "sleep 1d"),
		)
		sbmap = map[string]*sbcnt{defaultrt: nil, v1rt: nil}
	)
	_, err := previousProc.criImageService(t).PullImage(&runtime.ImageSpec{Image: testImage}, nil, nil, "")
	require.NoError(t, err)

	for name := range sbmap {
		t.Logf("Running sandbox with runtime %s", name)
		v2sbc := PodSandboxConfig(name, "")
		v2sbid, err := previousProc.criRuntimeService(t).RunPodSandbox(v2sbc, name)
		require.NoError(t, err)
		t.Logf("Create and start container with sandbox id %s", v2sbid)
		cn, err := previousProc.criRuntimeService(t).CreateContainer(v2sbid, cnConfig, v2sbc)
		require.NoError(t, err)
		require.NoError(t, previousProc.criRuntimeService(t).StartContainer(cn))
		sbmap[name] = &sbcnt{
			sbconfig: v2sbc,
			sbid:     v2sbid,
			cntid:    []string{cn},
		}
	}
	t.Log("Gracefully stop previous release's containerd process")
	require.NoError(t, previousProc.kill(syscall.SIGTERM))

	t.Logf("Install config for current containerd, containerd-shim-runc-v2, and old containerd-shim-runc-v1")
	ctrdcfg.reset(3)
	ctrdcfg.setRuntime(runtimeConfig{
		name:        defaultrt,
		runtimeType: "io.containerd.runc.v2",
		runtimePath: "",
	}, runtimeConfig{
		name:        v1rt,
		runtimeType: "io.containerd.runc.v2", // version 2.x not support runtime type: "io.containerd.runc.v1"
		runtimePath: filepath.Join(workDir, "bin", "containerd-shim-runc-v1"),
	})

	secondConfig := ContainerConfig(
		"test2",
		testImage,
		WithCommand("sh", "-c", "sleep 1d"),
	)

	t.Log("Starting the current release's containerd")
	currentProc := newCtrdProc(t, "containerd", workDir, nil)
	require.NoError(t, currentProc.isReady())
	_, err = currentProc.criImageService(t).PullImage(&runtime.ImageSpec{Image: testImage}, nil, nil, "")
	require.NoError(t, err)

	for _, sb := range sbmap {
		t.Logf("Create and start new container by current containerd process in sandbox %s", sb.sbid)
		cn, err := currentProc.criRuntimeService(t).CreateContainer(sb.sbid, secondConfig, sb.sbconfig)
		require.NoError(t, err)
		require.NoError(t, currentProc.criRuntimeService(t).StartContainer(cn))
		sb.cntid = append(sb.cntid, cn)
	}

	for _, sb := range sbmap {
		for _, cnid := range sb.cntid {
			t.Logf("Stop and remove container %s", cnid)
			require.NoError(t, currentProc.criRuntimeService(t).StopContainer(cnid, 10))
			require.NoError(t, currentProc.criRuntimeService(t).RemoveContainer(cnid))
			t.Logf("check container %s status dir, should not exist", cnid)
			_, err = os.Stat(path.Join(currentProc.rootPath(), "io.containerd.grpc.v1.cri", "containers", cnid))
			require.True(t, os.IsNotExist(err))
		}
		t.Logf("Stop and remove sandbox %s", sb.sbid)
		require.NoError(t, currentProc.criRuntimeService(t).StopPodSandbox(sb.sbid))
		require.NoError(t, currentProc.criRuntimeService(t).RemovePodSandbox(sb.sbid))
	}
	require.NoError(t, currentProc.kill(syscall.SIGTERM))
}

type ctrdConfig struct {
	t       *testing.T
	workdir string
	ver     int
}

type runtimeConfig struct {
	name        string
	runtimeType string
	runtimePath string
}

func newCtrdConfig(t *testing.T, ctrdWorkDir string, version int) *ctrdConfig {
	cc := &ctrdConfig{
		t:       t,
		workdir: ctrdWorkDir,
		ver:     version,
	}
	cc.reset(version)
	return cc
}

func (c *ctrdConfig) reset(version int) {
	fileName := filepath.Join(c.workdir, "config.toml")
	err := os.WriteFile(fileName, []byte(fmt.Sprintf("version = %d\n", version)), 0600)
	require.NoError(c.t, err, "failed to write config")
	c.ver = version
}

func (c *ctrdConfig) setRuntime(rcs ...runtimeConfig) {
	plugin := "io.containerd.grpc.v1.cri"
	temp := `

[plugins."%s".containerd.runtimes.%s]
  runtime_type = "%s"
  runtime_path = "%s"

	`
	if c.ver == 3 {
		plugin = "io.containerd.cri.v1.runtime"
	}
	f, err := os.OpenFile(filepath.Join(c.workdir, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(c.t, err, "failed to open config")
	for _, rc := range rcs {
		_, err = f.WriteString(fmt.Sprintf(temp, plugin, rc.name, rc.runtimeType, rc.runtimePath))
		require.NoError(c.t, err, "failed to write runtime config")
	}
	err = f.Close()
	require.NoError(c.t, err, "failed to close config")
}
