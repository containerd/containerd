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

package server

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/sandbox"
	crinri "github.com/containerd/containerd/v2/internal/cri/nri"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	nri "github.com/containerd/containerd/v2/internal/nri"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/errdefs"
)

type stopTestCRIImplementation struct {
	crinri.CRIImplementation
}

func (stopTestCRIImplementation) SandboxMetadataStore() sandbox.Store {
	return stopTestSandboxMetadataStore{}
}

type stopTestSandboxMetadataStore struct {
	sandbox.Store
}

func (stopTestSandboxMetadataStore) Get(context.Context, string) (sandbox.Sandbox, error) {
	return sandbox.Sandbox{}, errdefs.ErrNotFound
}

type stopTestNRI struct {
	nri.API
	blockPluginSyncCalls chan struct{}
	stopContainerCalls   chan struct{}
}

func (n *stopTestNRI) IsEnabled() bool { return true }

func (n *stopTestNRI) BlockPluginSync() *nri.PluginSyncBlock {
	n.blockPluginSyncCalls <- struct{}{}
	return nil
}

func (n *stopTestNRI) StopContainer(context.Context, nri.PodSandbox, nri.Container) error {
	n.stopContainerCalls <- struct{}{}
	return nil
}

type stopTestTask struct {
	containerd.Task
	termSent chan struct{}
}

func (t *stopTestTask) Pid() uint32 { return 1 }

func (t *stopTestTask) Kill(_ context.Context, sig syscall.Signal, _ ...containerd.KillOpts) error {
	if sig == syscall.SIGTERM {
		t.termSent <- struct{}{}
	}
	return nil
}

type stopTestContainer struct {
	containerd.Container
	id   string
	task containerd.Task
}

func (c *stopTestContainer) ID() string { return c.id }

func (c *stopTestContainer) Info(context.Context, ...containerd.InfoOpts) (containers.Container, error) {
	return containers.Container{
		ID:      c.id,
		Runtime: containers.RuntimeInfo{Name: "test"},
	}, nil
}

func (c *stopTestContainer) Spec(context.Context) (*oci.Spec, error) {
	return &oci.Spec{}, nil
}

func (c *stopTestContainer) Task(context.Context, cio.Attach) (containerd.Task, error) {
	return c.task, nil
}

func TestStopContainerDoesNotBlockPluginSyncDuringWait(t *testing.T) {
	const containerID = "test-container"

	c := newTestCRIService()
	nriAPI := &stopTestNRI{
		blockPluginSyncCalls: make(chan struct{}, 2),
		stopContainerCalls:   make(chan struct{}, 1),
	}
	c.nri = crinri.NewAPI(nriAPI, stopTestCRIImplementation{})
	require.NoError(t, c.sandboxStore.Add(sandboxstore.NewSandbox(
		sandboxstore.Metadata{ID: "test-sandbox", Config: &runtime.PodSandboxConfig{}},
		sandboxstore.Status{State: sandboxstore.StateReady},
	)))

	task := &stopTestTask{termSent: make(chan struct{}, 1)}
	ctrd := &stopTestContainer{id: containerID, task: task}
	container, err := containerstore.NewContainer(
		containerstore.Metadata{
			ID:         containerID,
			SandboxID:  "test-sandbox",
			Config:     &runtime.ContainerConfig{},
			StopSignal: "SIGTERM",
		},
		containerstore.WithContainer(ctrd),
		containerstore.WithFakeStatus(containerstore.Status{
			CreatedAt: time.Now().UnixNano(),
			StartedAt: time.Now().UnixNano(),
		}),
	)
	require.NoError(t, err)
	require.NoError(t, c.containerStore.Add(container))

	stopDone := make(chan error, 1)
	go func() {
		_, err := c.StopContainer(context.Background(), &runtime.StopContainerRequest{
			ContainerId: containerID,
			Timeout:     5,
		})
		stopDone <- err
	}()

	select {
	case <-task.termSent:
	case <-time.After(time.Second):
		t.Fatal("StopContainer did not send SIGTERM")
	}

	blockPluginSyncCalls := 0
	select {
	case <-nriAPI.blockPluginSyncCalls:
		blockPluginSyncCalls++
	default:
	}
	assert.Zero(t, blockPluginSyncCalls, "plugin synchronization must not be blocked while waiting for the container to stop")

	// Let the stop finish after checking whether plugin synchronization was
	// blocked during the wait for the container to exit.
	container.Stop()
	select {
	case err := <-stopDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("StopContainer did not finish after the container stopped")
	}

	select {
	case <-nriAPI.blockPluginSyncCalls:
		blockPluginSyncCalls++
	default:
	}
	assert.Equal(t, 1, blockPluginSyncCalls, "plugin synchronization should be blocked once, for the NRI stop notification")
	assert.Len(t, nriAPI.stopContainerCalls, 1, "NRI should receive one stop notification")
}

func TestWaitContainerStop(t *testing.T) {
	id := "test-id"
	for _, test := range []struct {
		desc      string
		status    *containerstore.Status
		cancel    bool
		timeout   time.Duration
		expectErr bool
	}{
		{
			desc: "should return error if timeout exceeds",
			status: &containerstore.Status{
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			timeout:   200 * time.Millisecond,
			expectErr: true,
		},
		{
			desc: "should return error if context is cancelled",
			status: &containerstore.Status{
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			timeout:   time.Hour,
			cancel:    true,
			expectErr: true,
		},
		{
			desc: "should not return error if container is stopped before timeout",
			status: &containerstore.Status{
				CreatedAt:  time.Now().UnixNano(),
				StartedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
			},
			timeout:   time.Hour,
			expectErr: false,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			c := newTestCRIService()
			container, err := containerstore.NewContainer(
				containerstore.Metadata{ID: id},
				containerstore.WithFakeStatus(*test.status),
			)
			assert.NoError(t, err)
			assert.NoError(t, c.containerStore.Add(container))
			ctx := context.Background()
			if test.cancel {
				cancelledCtx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelledCtx
			}
			if test.timeout > 0 {
				timeoutCtx, cancel := context.WithTimeout(ctx, test.timeout)
				defer cancel()
				ctx = timeoutCtx
			}
			err = c.waitContainerStop(ctx, container)
			assert.Equal(t, test.expectErr, err != nil, test.desc)
		})
	}
}

func TestCRISignalToOCIStopSignal(t *testing.T) {
	tests := []struct {
		name           string
		signal         runtime.Signal
		expectedSignal string
		expectErr      bool
	}{
		{name: "runtime default", signal: runtime.Signal_SIGNAL_RUNTIME_DEFAULT, expectedSignal: ""},
		{name: "standard signal", signal: runtime.Signal_SIGNAL_SIGTERM, expectedSignal: "SIGTERM"},
		{name: "rtmin plus", signal: runtime.Signal_SIGNAL_SIGRTMINPLUS1, expectedSignal: "SIGRTMIN+1"},
		{name: "rtmax minus", signal: runtime.Signal_SIGNAL_SIGRTMAXMINUS1, expectedSignal: "SIGRTMAX-1"},
		{name: "unknown signal", signal: runtime.Signal(999), expectErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stopSignal, err := criSignalToOCIStopSignal(test.signal)
			assert.Equal(t, test.expectErr, err != nil)
			assert.Equal(t, test.expectedSignal, stopSignal)
		})
	}
}

func TestConvertFromCRISignal(t *testing.T) {
	tests := []struct {
		name           string
		signal         string
		expectedSignal string
	}{
		{name: "standard signal", signal: "SIGTERM", expectedSignal: "SIGTERM"},
		{name: "rtmin plus", signal: "SIGRTMINPLUS1", expectedSignal: "SIGRTMIN+1"},
		{name: "rtmax minus", signal: "SIGRTMAXMINUS1", expectedSignal: "SIGRTMAX-1"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expectedSignal, convertFromCRISignal(test.signal))
		})
	}
}
