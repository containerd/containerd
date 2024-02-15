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
)

// upgradeVerifyCaseFunc is used to verify the behavior after upgrade.
type upgradeVerifyCaseFunc func(t *testing.T, criRuntimeService cri.RuntimeService, criImageService cri.ImageManagerService)

// TODO: Support Windows
func TestUpgrade(t *testing.T) {
	previousReleaseBinDir := t.TempDir()
	downloadPreviousReleaseBinary(t, previousReleaseBinDir)

	t.Run("recover", runUpgradeTestCase(previousReleaseBinDir, shouldRecoverAllThePodsAfterUpgrade))
	t.Run("exec", runUpgradeTestCase(previousReleaseBinDir, execToExistingContainer))
	t.Run("manipulate", runUpgradeTestCase(previousReleaseBinDir, shouldManipulateContainersInPodAfterUpgrade))
	t.Run("recover-images", runUpgradeTestCase(previousReleaseBinDir, shouldRecoverExistingImages))
	// TODO:
	// Add stats/stop-existing-running-pods/...
}

func runUpgradeTestCase(
	previousReleaseBinDir string,
	setupUpgradeVerifyCase func(t *testing.T, criRuntimeService cri.RuntimeService, criImageService cri.ImageManagerService) upgradeVerifyCaseFunc,
) func(t *testing.T) {
	return func(t *testing.T) {
		workDir := t.TempDir()

		t.Log("Install config for previous release")
		previousReleaseCtrdConfig(t, previousReleaseBinDir, workDir)

		t.Log("Starting the previous release's containerd")
		previousCtrdBinPath := filepath.Join(previousReleaseBinDir, "bin", "containerd")
		previousProc := newCtrdProc(t, previousCtrdBinPath, workDir)

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
		upgradeCaseFunc := setupUpgradeVerifyCase(t, previousProc.criRuntimeService(t), previousProc.criImageService(t))
		needToCleanup = false

		t.Log("Gracefully stop previous release's containerd process")
		require.NoError(t, previousProc.kill(syscall.SIGTERM))
		require.NoError(t, previousProc.wait(5*time.Minute))

		t.Log("Install default config for current release")
		currentReleaseCtrdDefaultConfig(t, workDir)

		t.Log("Starting the current release's containerd")
		currentProc := newCtrdProc(t, "containerd", workDir)
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

func shouldRecoverAllThePodsAfterUpgrade(t *testing.T, criRuntimeService cri.RuntimeService, criImageService cri.ImageManagerService) upgradeVerifyCaseFunc {
	var busyboxImage = images.Get(images.BusyBox)

	t.Logf("Pulling image %q", busyboxImage)
	_, err := criImageService.PullImage(&criruntime.ImageSpec{Image: busyboxImage}, nil, nil, "")
	require.NoError(t, err)

	t.Log("Create first sandbox")
	firstSBConfig := PodSandboxConfig("sandbox", "running-pod")
	firstSB, err := criRuntimeService.RunPodSandbox(firstSBConfig, "")
	require.NoError(t, err)

	t.Logf("Create a container config and run container in first pod")
	containerConfig := ContainerConfig("running", busyboxImage, WithCommand("sleep", "1d"))
	cn1InFirstSB, err := criRuntimeService.CreateContainer(firstSB, containerConfig, firstSBConfig)
	require.NoError(t, err)
	require.NoError(t, criRuntimeService.StartContainer(cn1InFirstSB))

	t.Logf("Just create a container in first pod")
	containerConfig = ContainerConfig("created", busyboxImage)
	cn2InFirstSB, err := criRuntimeService.CreateContainer(firstSB, containerConfig, firstSBConfig)
	require.NoError(t, err)

	t.Logf("Just create stopped container in first pod")
	containerConfig = ContainerConfig("stopped", busyboxImage, WithCommand("sleep", "1d"))
	cn3InFirstSB, err := criRuntimeService.CreateContainer(firstSB, containerConfig, firstSBConfig)
	require.NoError(t, err)
	require.NoError(t, criRuntimeService.StartContainer(cn3InFirstSB))
	require.NoError(t, criRuntimeService.StopContainer(cn3InFirstSB, 0))

	t.Log("Create second sandbox")
	secondSBConfig := PodSandboxConfig("sandbox", "stopped-pod")
	secondSB, err := criRuntimeService.RunPodSandbox(secondSBConfig, "")
	require.NoError(t, err)
	t.Log("Stop second sandbox")
	require.NoError(t, criRuntimeService.StopPodSandbox(secondSB))

	return func(t *testing.T, criRuntimeService cri.RuntimeService, _ cri.ImageManagerService) {
		t.Log("List Pods")

		pods, err := criRuntimeService.ListPodSandbox(nil)
		require.NoError(t, err)
		require.Len(t, pods, 2)

		for _, pod := range pods {
			t.Logf("Checking pod %s", pod.Id)
			switch pod.Id {
			case firstSB:
				assert.Equal(t, criruntime.PodSandboxState_SANDBOX_READY, pod.State)

				cntrs, err := criRuntimeService.ListContainers(&criruntime.ContainerFilter{
					PodSandboxId: pod.Id,
				})
				require.NoError(t, err)
				require.Equal(t, 3, len(cntrs))

				for _, cntr := range cntrs {
					switch cntr.Id {
					case cn1InFirstSB:
						assert.Equal(t, criruntime.ContainerState_CONTAINER_RUNNING, cntr.State)
					case cn2InFirstSB:
						assert.Equal(t, criruntime.ContainerState_CONTAINER_CREATED, cntr.State)
					case cn3InFirstSB:
						assert.Equal(t, criruntime.ContainerState_CONTAINER_EXITED, cntr.State)
					default:
						t.Errorf("unexpected container %s in %s", cntr.Id, pod.Id)
					}

				}

			case secondSB:
				assert.Equal(t, criruntime.PodSandboxState_SANDBOX_NOTREADY, pod.State)
			default:
				t.Errorf("unexpected pod %s", pod.Id)
			}
		}
	}
}

func execToExistingContainer(t *testing.T, criRuntimeService cri.RuntimeService, criImageService cri.ImageManagerService) upgradeVerifyCaseFunc {
	var busyboxImage = images.Get(images.BusyBox)

	t.Logf("Pulling image %q", busyboxImage)
	_, err := criImageService.PullImage(&criruntime.ImageSpec{Image: busyboxImage}, nil, nil, "")
	require.NoError(t, err)
	t.Log("Create sandbox")
	sbConfig := PodSandboxConfig("sandbox", "running")
	sbConfig.LogDirectory = t.TempDir()
	sb, err := criRuntimeService.RunPodSandbox(sbConfig, "")
	require.NoError(t, err)

	t.Logf("Create a running container")
	containerConfig := ContainerConfig("running", busyboxImage, WithCommand("sh", "-c", "while true; do date; sleep 1; done"))
	containerConfig.LogPath = "running#0.log"
	cntr, err := criRuntimeService.CreateContainer(sb, containerConfig, sbConfig)
	require.NoError(t, err)
	require.NoError(t, criRuntimeService.StartContainer(cntr))

	// NOTE: Wait for containerd to flush data into log
	time.Sleep(2 * time.Second)

	return func(t *testing.T, criRuntimeService cri.RuntimeService, _ cri.ImageManagerService) {
		pods, err := criRuntimeService.ListPodSandbox(nil)
		require.NoError(t, err)
		require.Len(t, pods, 1)

		cntrs, err := criRuntimeService.ListContainers(&criruntime.ContainerFilter{
			PodSandboxId: pods[0].Id,
		})
		require.NoError(t, err)
		require.Equal(t, 1, len(cntrs))
		assert.Equal(t, criruntime.ContainerState_CONTAINER_RUNNING, cntrs[0].State)

		func() {
			logPath := filepath.Join(sbConfig.LogDirectory, "running#0.log")

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
		}()

		t.Log("Run ExecSync")
		stdout, stderr, err := criRuntimeService.ExecSync(cntrs[0].Id, []string{"echo", "-n", "true"}, 0)
		require.NoError(t, err)
		require.Len(t, stderr, 0)
		require.Equal(t, "true", string(stdout))
	}
}

// getFileSize returns file's size.
func getFileSize(t *testing.T, filePath string) int64 {
	st, err := os.Stat(filePath)
	require.NoError(t, err)
	return st.Size()
}

func shouldManipulateContainersInPodAfterUpgrade(t *testing.T, criRuntimeService cri.RuntimeService, criImageService cri.ImageManagerService) upgradeVerifyCaseFunc {
	var busyboxImage = images.Get(images.BusyBox)

	t.Logf("Pulling image %q", busyboxImage)
	_, err := criImageService.PullImage(&criruntime.ImageSpec{Image: busyboxImage}, nil, nil, "")
	require.NoError(t, err)

	t.Log("Create a sandbox")
	sbConfig := PodSandboxConfig("sandbox", "running-pod")
	sb, err := criRuntimeService.RunPodSandbox(sbConfig, "")
	require.NoError(t, err)

	t.Logf("Create a container config and run container in the pod")
	containerConfig := ContainerConfig("running", busyboxImage, WithCommand("sleep", "1d"))
	cn1, err := criRuntimeService.CreateContainer(sb, containerConfig, sbConfig)
	require.NoError(t, err)
	require.NoError(t, criRuntimeService.StartContainer(cn1))

	t.Logf("Just create a container in the pod")
	containerConfig = ContainerConfig("created", busyboxImage)
	cn2, err := criRuntimeService.CreateContainer(sb, containerConfig, sbConfig)
	require.NoError(t, err)

	t.Logf("Just create stopped container in the pod")
	containerConfig = ContainerConfig("stopped", busyboxImage, WithCommand("sleep", "1d"))
	cn3, err := criRuntimeService.CreateContainer(sb, containerConfig, sbConfig)
	require.NoError(t, err)
	require.NoError(t, criRuntimeService.StartContainer(cn3))
	require.NoError(t, criRuntimeService.StopContainer(cn3, 0))

	return func(t *testing.T, criRuntimeService cri.RuntimeService, _ cri.ImageManagerService) {
		t.Log("Manipulating containers in the previous pod")
		// For the running container, we get status and stats of it,
		// exec and execsync in it, stop and remove it
		status, err := criRuntimeService.ContainerStatus(cn1)
		require.NoError(t, err)
		assert.Equal(t, status.State, criruntime.ContainerState_CONTAINER_RUNNING)
		_, err = criRuntimeService.ContainerStats(cn1)
		require.NoError(t, err)
		_, err = criRuntimeService.Exec(&criruntime.ExecRequest{
			ContainerId: cn1,
			Cmd:         []string{"/bin/sh"},
			Stderr:      false,
			Stdout:      true,
			Stdin:       true,
			Tty:         true,
		})
		require.NoError(t, err)
		require.NoError(t, criRuntimeService.StopContainer(cn1, 0))
		status, err = criRuntimeService.ContainerStatus(cn1)
		require.NoError(t, err)
		assert.Equal(t, status.State, criruntime.ContainerState_CONTAINER_EXITED)
		require.NoError(t, criRuntimeService.RemoveContainer(cn1))

		// For the created container, we start it, stop it and remove it
		status, err = criRuntimeService.ContainerStatus(cn2)
		require.NoError(t, err)
		assert.Equal(t, status.State, criruntime.ContainerState_CONTAINER_CREATED)
		require.NoError(t, criRuntimeService.StartContainer(cn2))
		status, err = criRuntimeService.ContainerStatus(cn2)
		require.NoError(t, err)
		assert.Equal(t, status.State, criruntime.ContainerState_CONTAINER_RUNNING)
		require.NoError(t, criRuntimeService.StopContainer(cn2, 0))
		status, err = criRuntimeService.ContainerStatus(cn2)
		require.NoError(t, err)
		assert.Equal(t, status.State, criruntime.ContainerState_CONTAINER_EXITED)
		require.NoError(t, criRuntimeService.RemoveContainer(cn2))

		// For the stopped container, we remove it
		status, err = criRuntimeService.ContainerStatus(cn3)
		require.NoError(t, err)
		assert.Equal(t, status.State, criruntime.ContainerState_CONTAINER_EXITED)
		require.NoError(t, criRuntimeService.RemoveContainer(cn3))

		// Create a new container in the previous pod, start, stop, and remove it
		t.Logf("Create a container config and run container in the previous pod")
		containerConfig = ContainerConfig("runinpreviouspod", busyboxImage, WithCommand("sleep", "1d"))
		cn4, err := criRuntimeService.CreateContainer(sb, containerConfig, sbConfig)
		require.NoError(t, err)
		require.NoError(t, criRuntimeService.StartContainer(cn4))
		status, err = criRuntimeService.ContainerStatus(cn4)
		require.NoError(t, err)
		assert.Equal(t, status.State, criruntime.ContainerState_CONTAINER_RUNNING)
		require.NoError(t, criRuntimeService.StopContainer(cn4, 0))
		require.NoError(t, criRuntimeService.RemoveContainer(cn4))
	}
}

func shouldRecoverExistingImages(t *testing.T, criRuntimeService cri.RuntimeService, criImageService cri.ImageManagerService) upgradeVerifyCaseFunc {
	images := []string{images.Get(images.BusyBox), images.Get(images.Alpine)}

	expectedRefs := []string{}
	for _, img := range images {
		t.Logf("Pulling image %q", img)
		imgRef, err := criImageService.PullImage(&criruntime.ImageSpec{Image: img}, nil, nil, "")
		require.NoError(t, err)
		expectedRefs = append(expectedRefs, imgRef)
	}

	return func(t *testing.T, _ cri.RuntimeService, criImageService cri.ImageManagerService) {
		t.Log("List all images")
		res, err := criImageService.ListImages(nil)
		require.NoError(t, err)
		require.Len(t, res, 2)

		for idx, img := range images {
			t.Logf("Check image %s status", img)
			gotImg, err := criImageService.ImageStatus(&criruntime.ImageSpec{Image: img})
			require.NoError(t, err)
			require.Equal(t, expectedRefs[idx], gotImg.Id)
		}
	}
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
func newCtrdProc(t *testing.T, ctrdBin string, ctrdWorkDir string) *ctrdProc {
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
