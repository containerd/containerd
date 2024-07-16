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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	criruntime "k8s.io/cri-api/pkg/apis/runtime/v1"

	cri "github.com/containerd/containerd/v2/integration/cri-api/pkg/apis"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/integration/remote"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

// upgradeVerifyCaseFunc is used to verify the behavior after upgrade.
type upgradeVerifyCaseFunc func(*testing.T, cri.RuntimeService, cri.ImageManagerService)

// beforeUpgradeHookFunc is a hook before upgrade.
type beforeUpgradeHookFunc func(*testing.T)

// TODO: Support Windows
func TestUpgrade(t *testing.T) {
	previousReleaseBinDir := t.TempDir()
	downloadPreviousLatestReleaseBinary(t, previousReleaseBinDir)

	t.Run("recover", runUpgradeTestCase(previousReleaseBinDir, shouldRecoverAllThePodsAfterUpgrade))
	t.Run("exec", runUpgradeTestCase(previousReleaseBinDir, execToExistingContainer))
	t.Run("manipulate", runUpgradeTestCase(previousReleaseBinDir, shouldManipulateContainersInPodAfterUpgrade))
	t.Run("recover-images", runUpgradeTestCase(previousReleaseBinDir, shouldRecoverExistingImages))
	t.Run("metrics", runUpgradeTestCase(previousReleaseBinDir, shouldParseMetricDataCorrectly))
}

func runUpgradeTestCase(
	previousReleaseBinDir string,
	setupUpgradeVerifyCase func(*testing.T, cri.RuntimeService, cri.ImageManagerService) (upgradeVerifyCaseFunc, beforeUpgradeHookFunc),
) func(t *testing.T) {
	return func(t *testing.T) {
		// NOTE: Using t.TempDir() here is to ensure there are no leaky
		// mountpoint after test completed.
		workDir := t.TempDir()

		t.Log("Install config for previous release")
		previousReleaseCtrdConfig(t, previousReleaseBinDir, workDir)

		t.Log("Starting the previous release's containerd")
		previousCtrdBinPath := filepath.Join(previousReleaseBinDir, "bin", "containerd")
		previousProc := newCtrdProc(t, previousCtrdBinPath, workDir, nil)

		ctrdLogPath := previousProc.logPath()
		t.Cleanup(func() {
			dumpFileContent(t, ctrdLogPath)
		})

		require.NoError(t, previousProc.isReady())

		needToCleanup := true
		t.Cleanup(func() {
			if t.Failed() && needToCleanup {
				t.Logf("Try to cleanup leaky pods")
				cleanupPods(t, previousProc.criRuntimeService(t))
			}
		})

		t.Log("Prepare pods for current release")
		upgradeCaseFunc, hookFunc := setupUpgradeVerifyCase(t, previousProc.criRuntimeService(t), previousProc.criImageService(t))
		needToCleanup = false

		t.Log("Gracefully stop previous release's containerd process")
		require.NoError(t, previousProc.kill(syscall.SIGTERM))
		require.NoError(t, previousProc.wait(5*time.Minute))

		if hookFunc != nil {
			t.Log("Run hook before upgrade")
			hookFunc(t)
		}

		t.Log("Install default config for current release")
		currentReleaseCtrdDefaultConfig(t, workDir)

		t.Log("Starting the current release's containerd")
		currentProc := newCtrdProc(t, "containerd", workDir, nil)
		require.NoError(t, currentProc.isReady())
		t.Cleanup(func() {
			t.Log("Cleanup all the pods")
			cleanupPods(t, currentProc.criRuntimeService(t))

			t.Log("Stopping current release's containerd process")
			require.NoError(t, currentProc.kill(syscall.SIGTERM))
			require.NoError(t, currentProc.wait(5*time.Minute))
		})

		t.Log("Verifing")
		upgradeCaseFunc(t, currentProc.criRuntimeService(t), currentProc.criImageService(t))
	}
}

func shouldRecoverAllThePodsAfterUpgrade(t *testing.T,
	rSvc cri.RuntimeService, iSvc cri.ImageManagerService) (upgradeVerifyCaseFunc, beforeUpgradeHookFunc) {

	var busyboxImage = images.Get(images.BusyBox)

	pullImagesByCRI(t, iSvc, busyboxImage)

	firstPodCtx := newPodTCtx(t, rSvc, "running-pod", "sandbox")

	cn1InFirstPod := firstPodCtx.createContainer("running", busyboxImage,
		criruntime.ContainerState_CONTAINER_RUNNING,
		WithCommand("sleep", "1d"))

	cn2InFirstPod := firstPodCtx.createContainer("created", busyboxImage,
		criruntime.ContainerState_CONTAINER_CREATED)

	cn3InFirstPod := firstPodCtx.createContainer("stopped", busyboxImage,
		criruntime.ContainerState_CONTAINER_EXITED,
		WithCommand("sleep", "1d"),
	)

	secondPodCtx := newPodTCtx(t, rSvc, "stopped-pod", "sandbox")
	secondPodCtx.stop(false)

	thirdPodCtx := newPodTCtx(t, rSvc, "kill-before-upgrade", "failpoint")
	thirdPodCtx.createContainer("sorry", busyboxImage,
		criruntime.ContainerState_CONTAINER_RUNNING,
		WithCommand("sleep", "3d"))

	thirdPodShimPid := int(thirdPodCtx.shimPid())

	hookFunc := func(t *testing.T) {
		// Kill the shim after stop previous containerd process
		syscall.Kill(thirdPodShimPid, syscall.SIGKILL)
	}

	return func(t *testing.T, rSvc cri.RuntimeService, _ cri.ImageManagerService) {
		t.Log("List Pods")

		pods, err := rSvc.ListPodSandbox(nil)
		require.NoError(t, err)
		require.Len(t, pods, 3)

		for _, pod := range pods {
			t.Logf("Checking pod %s", pod.Id)
			switch pod.Id {
			case firstPodCtx.id:
				assert.Equal(t, criruntime.PodSandboxState_SANDBOX_READY, pod.State)

				cntrs, err := rSvc.ListContainers(&criruntime.ContainerFilter{
					PodSandboxId: pod.Id,
				})
				require.NoError(t, err)
				require.Equal(t, 3, len(cntrs))

				for _, cntr := range cntrs {
					switch cntr.Id {
					case cn1InFirstPod:
						assert.Equal(t, criruntime.ContainerState_CONTAINER_RUNNING, cntr.State)
					case cn2InFirstPod:
						assert.Equal(t, criruntime.ContainerState_CONTAINER_CREATED, cntr.State)
					case cn3InFirstPod:
						assert.Equal(t, criruntime.ContainerState_CONTAINER_EXITED, cntr.State)
					default:
						t.Errorf("unexpected container %s in %s", cntr.Id, pod.Id)
					}
				}

			case secondPodCtx.id:
				assert.Equal(t, criruntime.PodSandboxState_SANDBOX_NOTREADY, pod.State)

			case thirdPodCtx.id:
				assert.Equal(t, criruntime.PodSandboxState_SANDBOX_NOTREADY, pod.State)

				cntrs, err := rSvc.ListContainers(&criruntime.ContainerFilter{
					PodSandboxId: pod.Id,
				})
				require.NoError(t, err)
				require.Equal(t, 1, len(cntrs))
				assert.Equal(t, criruntime.ContainerState_CONTAINER_EXITED, cntrs[0].State)

			default:
				t.Errorf("unexpected pod %s", pod.Id)
			}
		}
	}, hookFunc
}

func execToExistingContainer(t *testing.T,
	rSvc cri.RuntimeService, iSvc cri.ImageManagerService) (upgradeVerifyCaseFunc, beforeUpgradeHookFunc) {

	var busyboxImage = images.Get(images.BusyBox)

	pullImagesByCRI(t, iSvc, busyboxImage)

	podLogDir := t.TempDir()
	podCtx := newPodTCtx(t, rSvc, "running", "sandbox", WithPodLogDirectory(podLogDir))

	cntrLogName := "running#0.log"
	cnID := podCtx.createContainer("running", busyboxImage,
		criruntime.ContainerState_CONTAINER_RUNNING,
		WithCommand("sh", "-c", "while true; do date; sleep 1; done"),
		WithLogPath(cntrLogName),
	)

	// NOTE: Wait for containerd to flush data into log
	time.Sleep(2 * time.Second)

	return func(t *testing.T, rSvc cri.RuntimeService, _ cri.ImageManagerService) {
		pods, err := rSvc.ListPodSandbox(nil)
		require.NoError(t, err)
		require.Len(t, pods, 1)

		cntrs, err := rSvc.ListContainers(&criruntime.ContainerFilter{
			PodSandboxId: pods[0].Id,
		})
		require.NoError(t, err)
		require.Equal(t, 1, len(cntrs))
		assert.Equal(t, criruntime.ContainerState_CONTAINER_RUNNING, cntrs[0].State)
		assert.Equal(t, cnID, cntrs[0].Id)

		logPath := filepath.Join(podLogDir, cntrLogName)

		// NOTE: containerd should recover container's IO as well
		t.Logf("Check container's log %s", logPath)

		logSizeChange := false
		curSize := getFileSize(t, logPath)
		for i := 0; i < 30; i++ {
			time.Sleep(1 * time.Second)

			if curSize < getFileSize(t, logPath) {
				logSizeChange = true
				break
			}
		}
		require.True(t, logSizeChange)

		t.Log("Run ExecSync")
		stdout, stderr, err := rSvc.ExecSync(cntrs[0].Id, []string{"echo", "-n", "true"}, 0)
		require.NoError(t, err)
		require.Len(t, stderr, 0)
		require.Equal(t, "true", string(stdout))
	}, nil
}

// getFileSize returns file's size.
func getFileSize(t *testing.T, filePath string) int64 {
	st, err := os.Stat(filePath)
	require.NoError(t, err)
	return st.Size()
}

func shouldManipulateContainersInPodAfterUpgrade(t *testing.T,
	rSvc cri.RuntimeService, iSvc cri.ImageManagerService) (upgradeVerifyCaseFunc, beforeUpgradeHookFunc) {

	var busyboxImage = images.Get(images.BusyBox)

	pullImagesByCRI(t, iSvc, busyboxImage)

	podCtx := newPodTCtx(t, rSvc, "running-pod", "sandbox")

	cntr1 := podCtx.createContainer("running", busyboxImage,
		criruntime.ContainerState_CONTAINER_RUNNING,
		WithCommand("sleep", "1d"))

	cntr2 := podCtx.createContainer("created", busyboxImage,
		criruntime.ContainerState_CONTAINER_CREATED,
		WithCommand("sleep", "1d"))

	cntr3 := podCtx.createContainer("stopped", busyboxImage,
		criruntime.ContainerState_CONTAINER_EXITED,
		WithCommand("sleep", "1d"))

	return func(t *testing.T, rSvc cri.RuntimeService, _ cri.ImageManagerService) {
		// TODO(fuweid): make svc re-connect to new socket
		podCtx.rSvc = rSvc

		t.Log("Manipulating containers in the previous pod")

		// For the running container, we get status and stats of it,
		// exec and execsync in it, stop and remove it
		checkContainerState(t, rSvc, cntr1, criruntime.ContainerState_CONTAINER_RUNNING)

		t.Logf("Checking container %s's stats", cntr1)
		stats, err := rSvc.ContainerStats(cntr1)
		require.NoError(t, err)
		require.True(t, stats.GetMemory().GetWorkingSetBytes().GetValue() > 0)

		t.Logf("Preparing attachable exec for container %s", cntr1)
		_, err = rSvc.Exec(&criruntime.ExecRequest{
			ContainerId: cntr1,
			Cmd:         []string{"/bin/sh"},
			Stderr:      false,
			Stdout:      true,
			Stdin:       true,
			Tty:         true,
		})
		require.NoError(t, err)

		t.Logf("Stopping container %s", cntr1)
		require.NoError(t, rSvc.StopContainer(cntr1, 0))
		checkContainerState(t, rSvc, cntr1, criruntime.ContainerState_CONTAINER_EXITED)

		cntr1DataDir := podCtx.containerDataDir(cntr1)
		t.Logf("Container %s's data dir %s should be remained until RemoveContainer", cntr1, cntr1DataDir)
		_, err = os.Stat(cntr1DataDir)
		require.NoError(t, err)

		t.Logf("Starting created container %s", cntr2)
		checkContainerState(t, rSvc, cntr2, criruntime.ContainerState_CONTAINER_CREATED)

		require.NoError(t, rSvc.StartContainer(cntr2))
		checkContainerState(t, rSvc, cntr2, criruntime.ContainerState_CONTAINER_RUNNING)

		t.Logf("Stopping running container %s", cntr2)
		require.NoError(t, rSvc.StopContainer(cntr2, 0))
		checkContainerState(t, rSvc, cntr2, criruntime.ContainerState_CONTAINER_EXITED)

		t.Logf("Removing exited container %s", cntr3)
		checkContainerState(t, rSvc, cntr3, criruntime.ContainerState_CONTAINER_EXITED)

		cntr3DataDir := podCtx.containerDataDir(cntr3)
		_, err = os.Stat(cntr3DataDir)
		require.NoError(t, err)

		require.NoError(t, rSvc.RemoveContainer(cntr3))

		t.Logf("Container %s's data dir %s should be deleted after RemoveContainer", cntr3, cntr3DataDir)
		_, err = os.Stat(cntr3DataDir)
		require.True(t, os.IsNotExist(err))

		// Create a new container in the previous pod, start, stop, and remove it
		podCtx.createContainer("runinpreviouspod", busyboxImage,
			criruntime.ContainerState_CONTAINER_EXITED,
			WithCommand("sleep", "1d"))

		podCtx.stop(true)
		podDataDir := podCtx.dataDir()

		t.Logf("Pod %s's data dir %s should be deleted", podCtx.id, podDataDir)
		_, err = os.Stat(podDataDir)
		require.True(t, os.IsNotExist(err))

		cntrDataDir := filepath.Dir(cntr3DataDir)
		t.Logf("Containers data dir %s should be empty", cntrDataDir)
		ents, err := os.ReadDir(cntrDataDir)
		require.NoError(t, err)
		require.Len(t, ents, 0, cntrDataDir)
	}, nil
}

func shouldRecoverExistingImages(t *testing.T,
	_ cri.RuntimeService, iSvc cri.ImageManagerService) (upgradeVerifyCaseFunc, beforeUpgradeHookFunc) {

	images := []string{images.Get(images.BusyBox), images.Get(images.Alpine)}
	expectedRefs := pullImagesByCRI(t, iSvc, images...)

	return func(t *testing.T, _ cri.RuntimeService, iSvc cri.ImageManagerService) {
		t.Log("List all images")
		res, err := iSvc.ListImages(nil)
		require.NoError(t, err)
		require.Len(t, res, 2)

		for idx, img := range images {
			t.Logf("Check image %s status", img)
			gotImg, err := iSvc.ImageStatus(&criruntime.ImageSpec{Image: img})
			require.NoError(t, err)
			require.Equal(t, expectedRefs[idx], gotImg.Id)
		}
	}, nil
}

// shouldParseMetricDataCorrectly is to check new release containerd can parse
// metric data from existing shim created by previous release.
func shouldParseMetricDataCorrectly(t *testing.T,
	rSvc cri.RuntimeService, iSvc cri.ImageManagerService) (upgradeVerifyCaseFunc, beforeUpgradeHookFunc) {

	imageName := images.Get(images.BusyBox)
	pullImagesByCRI(t, iSvc, imageName)

	scriptVolume := t.TempDir()
	scriptInHost := filepath.Join(scriptVolume, "run.sh")

	fileSize := 1024 * 1024 * 96 // 96 MiB
	require.NoError(t, os.WriteFile(scriptInHost, []byte(fmt.Sprintf(`#!/bin/sh
set -euo pipefail

head -c %d </dev/urandom >/tmp/log

# increase page cache usage
for i in {1..10}; do
  cat /tmp/log > /dev/null
done

echo "ready"

while true; do
  cat /tmp/log > /dev/null
  sleep 1
done
`, fileSize,
	),
	), 0600))

	podLogDir := t.TempDir()
	podCtx := newPodTCtx(t, rSvc, "running", "sandbox", WithPodLogDirectory(podLogDir))

	scriptInContainer := "/run.sh"
	cntrLogName := "running#0.log"

	cntr := podCtx.createContainer("app", imageName,
		criruntime.ContainerState_CONTAINER_RUNNING,
		WithCommand("sh", scriptInContainer),
		WithVolumeMount(scriptInHost, scriptInContainer),
		WithLogPath(cntrLogName),
	)

	return func(t *testing.T, rSvc cri.RuntimeService, _ cri.ImageManagerService) {
		checkContainerState(t, rSvc, cntr, criruntime.ContainerState_CONTAINER_RUNNING)

		logPath := filepath.Join(podLogDir, cntrLogName)

		t.Log("Warm-up page cache")
		isReady := false
		for i := 0; i < 30 && !isReady; i++ {
			data, err := os.ReadFile(logPath)
			require.NoError(t, err)

			isReady = strings.Contains(string(data), "ready")

			time.Sleep(1 * time.Second)
		}
		require.True(t, isReady, "warm-up page cache")

		stats, err := rSvc.ContainerStats(cntr)
		require.NoError(t, err)

		data, err := json.MarshalIndent(stats, "", "  ")
		require.NoError(t, err)
		t.Logf("Dump container %s's metric: \n%s", cntr, string(data))

		// NOTE: Just in case that part of inactive cache has been reclaimed.
		expectedBytes := uint64(fileSize * 2 / 3)
		require.True(t, stats.GetMemory().GetUsageBytes().GetValue() > expectedBytes)
	}, nil
}

func newPodTCtx(t *testing.T, rSvc cri.RuntimeService,
	name, ns string, opts ...PodSandboxOpts) *podTCtx {

	t.Logf("Run a sandbox %s in namespace %s", name, ns)
	sbConfig := PodSandboxConfig(name, ns, opts...)
	sbID, err := rSvc.RunPodSandbox(sbConfig, "")
	require.NoError(t, err)

	return &podTCtx{
		t:    t,
		id:   sbID,
		name: name,
		ns:   ns,
		cfg:  sbConfig,
		rSvc: rSvc,
	}
}

// podTCtx is used to construct pod.
type podTCtx struct {
	t    *testing.T
	id   string
	name string
	ns   string
	cfg  *criruntime.PodSandboxConfig
	rSvc cri.RuntimeService
}

// createContainer creates a container in that pod.
func (pCtx *podTCtx) createContainer(name, imageRef string, wantedState criruntime.ContainerState, opts ...ContainerOpts) string {
	t := pCtx.t

	t.Logf("Create a container %s (wantedState: %s) in pod %s", name, wantedState, pCtx.name)
	cfg := ContainerConfig(name, imageRef, opts...)
	cnID, err := pCtx.rSvc.CreateContainer(pCtx.id, cfg, pCtx.cfg)
	require.NoError(t, err)

	switch wantedState {
	case criruntime.ContainerState_CONTAINER_CREATED:
		// no-op
	case criruntime.ContainerState_CONTAINER_RUNNING:
		require.NoError(t, pCtx.rSvc.StartContainer(cnID))
	case criruntime.ContainerState_CONTAINER_EXITED:
		require.NoError(t, pCtx.rSvc.StartContainer(cnID))
		require.NoError(t, pCtx.rSvc.StopContainer(cnID, 0))
	default:
		t.Fatalf("unsupport state %s", wantedState)
	}
	return cnID
}

// containerDataDir returns container metadata dir maintained by CRI plugin.
func (pCtx *podTCtx) containerDataDir(cntrID string) string {
	t := pCtx.t

	// check if container exists
	status, err := pCtx.rSvc.ContainerStatus(cntrID)
	require.NoError(t, err)

	cfg := criRuntimeInfo(t, pCtx.rSvc)

	rootDir := cfg["rootDir"].(string)
	return filepath.Join(rootDir, "containers", status.Id)
}

// shimPid returns shim's pid.
func (pCtx *podTCtx) shimPid() uint32 {
	t := pCtx.t
	cfg := criRuntimeInfo(t, pCtx.rSvc)

	ctx := namespaces.WithNamespace(context.Background(), "k8s.io")

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// NOTE: use version 2 to be compatible with previous release
	shimCli := connectToShim(ctx, t, cfg["containerdEndpoint"].(string), 2, pCtx.id)
	return shimPid(ctx, t, shimCli)
}

// dataDir returns pod metadata dir maintained by CRI plugin.
func (pCtx *podTCtx) dataDir() string {
	t := pCtx.t

	cfg := criRuntimeInfo(t, pCtx.rSvc)
	rootDir := cfg["rootDir"].(string)
	return filepath.Join(rootDir, "sandboxes", pCtx.id)
}

// stop stops that pod.
func (pCtx *podTCtx) stop(remove bool) {
	t := pCtx.t

	t.Logf("Stopping pod %s", pCtx.id)
	require.NoError(t, pCtx.rSvc.StopPodSandbox(pCtx.id))
	if remove {
		t.Logf("Removing pod %s", pCtx.id)
		require.NoError(t, pCtx.rSvc.RemovePodSandbox(pCtx.id))
	}
}

// criRuntimeInfo dumps CRI config.
func criRuntimeInfo(t *testing.T, svc cri.RuntimeService) map[string]interface{} {
	resp, err := svc.Status()
	require.NoError(t, err)

	cfg := map[string]interface{}{}
	err = json.Unmarshal([]byte(resp.GetInfo()["config"]), &cfg)
	require.NoError(t, err)

	return cfg
}

// checkContainerState checks container's state.
func checkContainerState(t *testing.T, svc cri.RuntimeService, name string, expected criruntime.ContainerState) {
	t.Logf("Checking container %s state", name)
	status, err := svc.ContainerStatus(name)
	require.NoError(t, err)
	assert.Equal(t, expected, status.State)
}

// pullImagesByCRI pulls images by CRI.
func pullImagesByCRI(t *testing.T, svc cri.ImageManagerService, images ...string) []string {
	expectedRefs := make([]string, 0, len(images))

	for _, image := range images {
		t.Logf("Pulling image %q", image)
		imgRef, err := svc.PullImage(&criruntime.ImageSpec{Image: image}, nil, nil, "")
		require.NoError(t, err)
		expectedRefs = append(expectedRefs, imgRef)
	}
	return expectedRefs
}

// cleanupPods deletes all the pods based on the cri.RuntimeService connection.
func cleanupPods(t *testing.T, criRuntimeService cri.RuntimeService) {
	pods, err := criRuntimeService.ListPodSandbox(nil)
	require.NoError(t, err)

	for _, pod := range pods {
		assert.NoError(t, criRuntimeService.StopPodSandbox(pod.Id))
		assert.NoError(t, criRuntimeService.RemovePodSandbox(pod.Id))
	}
}

// currentReleaseCtrdDefaultConfig generates empty(default) config for current release.
func currentReleaseCtrdDefaultConfig(t *testing.T, targetDir string) {
	fileName := filepath.Join(targetDir, "config.toml")
	err := os.WriteFile(fileName, []byte(""), 0600)
	require.NoError(t, err, "failed to create config for current release")
}

// previousReleaseCtrdConfig generates containerd config with previous release
// shim binary.
func previousReleaseCtrdConfig(t *testing.T, previousReleaseBinDir, targetDir string) {
	// TODO(fuweid):
	//
	// We should choose correct config version based on previous release.
	// Currently, we're focusing on v1.x -> v2.0 so we use version = 2 here.
	rawCfg := fmt.Sprintf(`
version = 2

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
  runtime_type = "%s/bin/containerd-shim-runc-v2"
`,
		previousReleaseBinDir)

	fileName := filepath.Join(targetDir, "config.toml")
	err := os.WriteFile(fileName, []byte(rawCfg), 0600)
	require.NoError(t, err, "failed to create config for previous release")
}

// criRuntimeService returns cri.RuntimeService based on the grpc address.
func (p *ctrdProc) criRuntimeService(t *testing.T) cri.RuntimeService {
	service, err := remote.NewRuntimeService(p.grpcAddress(), 1*time.Minute)
	require.NoError(t, err)
	return service
}

// criImageService returns cri.ImageManagerService based on the grpc address.
func (p *ctrdProc) criImageService(t *testing.T) cri.ImageManagerService {
	service, err := remote.NewImageService(p.grpcAddress(), 1*time.Minute)
	require.NoError(t, err)
	return service
}

// newCtrdProc is to start containerd process.
func newCtrdProc(t *testing.T, ctrdBin string, ctrdWorkDir string, envs []string) *ctrdProc {
	p := &ctrdProc{workDir: ctrdWorkDir}

	var args []string
	args = append(args, "--root", p.rootPath())
	args = append(args, "--state", p.statePath())
	args = append(args, "--address", p.grpcAddress())
	args = append(args, "--config", p.configPath())
	args = append(args, "--log-level", "debug")

	f, err := os.OpenFile(p.logPath(), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0600)
	require.NoError(t, err, "open log file %s", p.logPath())
	t.Cleanup(func() { f.Close() })

	cmd := exec.Command(ctrdBin, args...)
	cmd.Env = append(os.Environ(), envs...)
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}

	p.cmd = cmd
	p.waitBlock = make(chan struct{})
	go func() {
		// The PDeathSIG is based on the thread which forks the child
		// process instead of the leader of thread group. Lock the
		// thread just in case that the thread exits and causes unexpected
		// SIGKILL to containerd.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		defer close(p.waitBlock)

		require.NoError(t, p.cmd.Start(), "start containerd(%s)", ctrdBin)
		assert.NoError(t, p.cmd.Wait())
	}()
	return p
}

// ctrdProc is used to control the containerd process's lifecycle.
type ctrdProc struct {
	// workDir has the following layout:
	//
	// - root  (dir)
	// - state (dir)
	// - containerd.sock (sock file)
	// - config.toml (toml file, required)
	// - containerd.log (log file, always open with O_APPEND)
	workDir   string
	cmd       *exec.Cmd
	waitBlock chan struct{}
}

// kill is to send the signal to containerd process.
func (p *ctrdProc) kill(sig syscall.Signal) error {
	return p.cmd.Process.Signal(sig)
}

// wait is to wait for exit event of containerd process.
func (p *ctrdProc) wait(to time.Duration) error {
	var ctx = context.Background()
	var cancel context.CancelFunc

	if to > 0 {
		ctx, cancel = context.WithTimeout(ctx, to)
		defer cancel()
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.waitBlock:
		return nil
	}
}

// grpcAddress is to return containerd's address.
func (p *ctrdProc) grpcAddress() string {
	return filepath.Join(p.workDir, "containerd.sock")
}

// configPath is to return containerd's config file.
func (p *ctrdProc) configPath() string {
	return filepath.Join(p.workDir, "config.toml")
}

// rootPath is to return containerd's root path.
func (p *ctrdProc) rootPath() string {
	return filepath.Join(p.workDir, "root")
}

// statePath is to return containerd's state path.
func (p *ctrdProc) statePath() string {
	return filepath.Join(p.workDir, "state")
}

// logPath is to return containerd's log path.
func (p *ctrdProc) logPath() string {
	return filepath.Join(p.workDir, "containerd.log")
}

// isReady checks the containerd is ready or not.
func (p *ctrdProc) isReady() error {
	var (
		service     cri.RuntimeService
		err         error
		ticker      = time.NewTicker(1 * time.Second)
		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Minute)
	)
	defer func() {
		cancel()
		ticker.Stop()
	}()

	for {
		select {
		case <-ticker.C:
			service, err = remote.NewRuntimeService(p.grpcAddress(), 5*time.Second)
			if err != nil {
				continue
			}

			if _, err = service.Status(); err != nil {
				continue
			}
			return nil
		case <-ctx.Done():
			return fmt.Errorf("context deadline exceeded: %w", err)
		}
	}
}

// dumpFileContent dumps file content into t.Log.
func dumpFileContent(t *testing.T, filePath string) {
	f, err := os.Open(filePath)
	require.NoError(t, err)
	defer f.Close()

	r := bufio.NewReader(f)
	for {
		line, err := r.ReadString('\n')
		switch err {
		case nil:
			t.Log(strings.TrimSuffix(line, "\n"))
		case io.EOF:
			return
		default:
			require.NoError(t, err)
		}
	}
}
