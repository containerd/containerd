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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	cri "github.com/containerd/containerd/integration/cri-api/pkg/apis"
	"github.com/containerd/containerd/integration/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	criruntime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

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

// pullImagesByCRI pulls images by CRI.
func pullImagesByCRI(t *testing.T, svc cri.ImageManagerService, images ...string) []string {
	expectedRefs := make([]string, 0, len(images))

	for _, image := range images {
		t.Logf("Pulling image %q", image)
		imgRef, err := svc.PullImage(&criruntime.ImageSpec{Image: image}, nil, nil)
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
