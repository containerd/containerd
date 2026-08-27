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
	"syscall"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	"github.com/stretchr/testify/require"

	eventtypes "github.com/containerd/containerd/api/events"
	apitask "github.com/containerd/containerd/api/runtime/task/v3"
	"github.com/containerd/containerd/v2/core/events"
	ctrdruntime "github.com/containerd/containerd/v2/core/runtime"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	ptypes "github.com/containerd/containerd/v2/pkg/protobuf/types"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// TestIssue7496_ShouldRetryShutdown is based on https://github.com/containerd/containerd/issues/7496.
//
// The first task Delete succeeds, but an injected Shutdown failure prevents the
// shim from exiting. The CRI retry then sees the task as NotFound and must still
// shut down the shim without publishing duplicate exit or delete events.
func TestIssue7496_ShouldRetryShutdown(t *testing.T) {
	const eventBarrierTopic = "/tests/issue7496/event-barrier"

	ctx := namespaces.WithNamespace(t.Context(), "k8s.io")

	t.Logf("Create a pod config with shutdown failpoint")
	sbConfig := PodSandboxConfig("sandbox", "issue7496_shouldretryshutdown", WithHostNetwork)
	injectShimFailpoint(t, sbConfig, map[string]string{
		"Shutdown": "1*error(please retry)",
	})

	t.Logf("RunPodSandbox")
	sbID, err := runtimeService.RunPodSandbox(sbConfig, failpointRuntimeHandler)
	require.NoError(t, err)

	t.Logf("Connect to the shim %s", sbID)
	shimCli := connectToShim(ctx, t, containerdEndpoint, 3, sbID)

	t.Logf("Log shim %s's pid: %d", sbID, shimPid(ctx, t, shimCli))

	t.Logf("Subscribe task exit/delete events")
	eventCh, eventErrCh := containerdClient.EventService().Subscribe(ctx,
		fmt.Sprintf(`topic=="%s",event.container_id=="%s"`, ctrdruntime.TaskExitEventTopic, sbID),
		fmt.Sprintf(`topic=="%s",event.container_id=="%s"`, ctrdruntime.TaskDeleteEventTopic, sbID),
		fmt.Sprintf(`topic=="%s"`, eventBarrierTopic),
	)

	t.Logf("StopPodSandbox and RemovePodSandbox")
	require.NoError(t, runtimeService.StopPodSandbox(sbID))
	require.NoError(t, runtimeService.RemovePodSandbox(sbID))

	t.Logf("Check the shim connection")
	_, err = shimCli.Connect(ctx, &apitask.ConnectRequest{})
	require.Error(t, err, "should fail to call the shim Connect API")
	require.ErrorContains(t, err, "ttrpc: closed")

	// The task delete succeeded on the first attempt, so the shim itself has
	// already delivered the exit and delete events. When the retry finally
	// shuts the shim down, ttrpc-callback-on-close must not publish them a
	// second time.
	//
	// REF: https://github.com/containerd/containerd/issues/4769
	t.Logf("Check that task exit/delete events are not duplicated")
	require.NoError(t, containerdClient.EventService().Publish(ctx,
		eventBarrierTopic, &ptypes.Empty{},
	))
	exits, deletes := countTaskExitDeleteEventsUntilBarrier(
		t, sbID, eventBarrierTopic, eventCh, eventErrCh, 10*time.Second,
	)
	require.Equal(t, 1, exits, "task exit event should be published only once")
	require.Equal(t, 1, deletes, "task delete event should be published only once")
}

func TestKillShimPublishesTaskExitAndDeleteEvents(t *testing.T) {
	ctx := namespaces.WithNamespace(t.Context(), "k8s.io")

	sbConfig := PodSandboxConfig("sandbox", t.Name(), WithHostNetwork)
	sbID, err := runtimeService.RunPodSandbox(sbConfig, "")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, runtimeService.StopPodSandbox(sbID))
		require.NoError(t, runtimeService.RemovePodSandbox(sbID))
	})

	shimCli := connectToShim(ctx, t, containerdEndpoint, 3, sbID)
	shimPID := shimPid(ctx, t, shimCli)

	eventCh, eventErrCh := containerdClient.EventService().Subscribe(ctx,
		fmt.Sprintf(`topic=="%s",event.container_id=="%s"`, ctrdruntime.TaskExitEventTopic, sbID),
		fmt.Sprintf(`topic=="%s",event.container_id=="%s"`, ctrdruntime.TaskDeleteEventTopic, sbID),
	)

	require.NoError(t, syscall.Kill(int(shimPID), syscall.SIGKILL))
	waitForTaskExitDeleteEvents(t, sbID, eventCh, eventErrCh, 10*time.Second)
}

func waitForTaskExitDeleteEvents(t *testing.T, id string,
	ch <-chan *events.Envelope, errCh <-chan error, timeout time.Duration,
) {
	t.Helper()

	pending := map[string]struct{}{
		ctrdruntime.TaskExitEventTopic:   {},
		ctrdruntime.TaskDeleteEventTopic: {},
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for len(pending) != 0 {
		select {
		case <-timer.C:
			t.Fatalf("timed out waiting for task exit/delete events for %q", id)
		case err, ok := <-errCh:
			if !ok {
				t.Fatalf("event subscription closed while waiting for %q", id)
			}
			require.NoError(t, err, "event subscription failed")
		case env, ok := <-ch:
			require.True(t, ok, "event subscription closed while waiting for %q", id)
			t.Logf("received event %q for %q", env.Topic, id)
			delete(pending, env.Topic)
		}
	}
}

// countTaskExitDeleteEventsUntilBarrier counts task exit and delete events for
// the given container ID until the event stream reaches barrierTopic. The
// barrier is published after shim deletion has waited for its close callback,
// so any duplicate events from cleanupAfterDeadShim are ordered before it.
func countTaskExitDeleteEventsUntilBarrier(t *testing.T, id, barrierTopic string,
	ch <-chan *events.Envelope, errCh <-chan error, timeout time.Duration,
) (exits, deletes int) {
	t.Helper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			t.Fatalf("timed out waiting for event barrier %q", barrierTopic)
		case err, ok := <-errCh:
			if !ok {
				t.Fatalf("event subscription closed before barrier %q", barrierTopic)
			}
			require.NoError(t, err, "event subscription failed")
		case env, ok := <-ch:
			require.True(t, ok, "event subscription closed before barrier %q", barrierTopic)
			if env.Topic == barrierTopic {
				return exits, deletes
			}

			evt, err := typeurl.UnmarshalAny(env.Event)
			require.NoError(t, err, "failed to unmarshal event")

			switch e := evt.(type) {
			case *eventtypes.TaskExit:
				if e.ContainerID == id {
					t.Logf("received task exit event: %+v", e)
					exits++
				}
			case *eventtypes.TaskDelete:
				if e.ContainerID == id {
					t.Logf("received task delete event: %+v", e)
					deletes++
				}
			}
		}
	}
}

func TestShutdownShimWhenPauseExitsBeforeWorkload(t *testing.T) {
	ctx := namespaces.WithNamespace(t.Context(), "k8s.io")

	t.Logf("RunPodSandbox")
	sbConfig := PodSandboxConfig("sandbox", t.Name(), WithHostNetwork)
	sbID, err := runtimeService.RunPodSandbox(sbConfig, "")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = runtimeService.StopPodSandbox(sbID)
		_ = runtimeService.RemovePodSandbox(sbID)
	})

	t.Logf("Connect to the shim %s", sbID)
	shimCli := connectToShim(ctx, t, containerdEndpoint, 3, sbID)

	testImage := images.Get(images.BusyBox)
	EnsureImageExists(t, testImage)

	t.Log("Create a container - sleep 1d")
	containerName := "test-container"
	cnConfig := ContainerConfig(
		containerName,
		testImage,
		WithCommand("sh", "-c", "sleep 1d"),
		WithPidNamespace(runtime.NamespaceMode_CONTAINER),
	)
	cnID, err := runtimeService.CreateContainer(sbID, cnConfig, sbConfig)
	require.NoError(t, err)

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cnID))

	t.Log("Load pause task and wait")
	pauseContainer, err := containerdClient.LoadContainer(ctx, sbID)
	require.NoError(t, err)
	pauseTask, err := pauseContainer.Task(ctx, nil)
	require.NoError(t, err)
	pauseExitCh, err := pauseTask.Wait(ctx)
	require.NoError(t, err)

	t.Log("Load workload task and wait")
	workloadContainer, err := containerdClient.LoadContainer(ctx, cnID)
	require.NoError(t, err)
	workloadTask, err := workloadContainer.Task(ctx, nil)
	require.NoError(t, err)
	workloadExitCh, err := workloadTask.Wait(ctx)
	require.NoError(t, err)

	t.Log("Kill pause container by containerd client API")
	require.NoError(t, pauseTask.Kill(ctx, syscall.SIGKILL))

	select {
	case status := <-pauseExitCh:
		pauseExitStatus, _, err := status.Result()
		require.NoError(t, err)
		require.Equal(t, uint32(137), pauseExitStatus)
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for pause task exit")
	}

	t.Log("Wait for pause task deletion")
	require.NoError(t, Eventually(func() (bool, error) {
		_, err := pauseContainer.Task(ctx, nil)
		if err == nil {
			return false, nil
		}

		if errdefs.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}, time.Second, 30*time.Second))

	t.Log("Stop sandbox and wait for workload")
	require.NoError(t, runtimeService.StopPodSandbox(sbID))

	select {
	case status := <-workloadExitCh:
		workloadExitStatus, _, err := status.Result()
		require.NoError(t, err)
		require.Equal(t, uint32(137), workloadExitStatus)
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for workload task exit")
	}

	t.Log("Remove sandbox")
	require.NoError(t, runtimeService.RemovePodSandbox(sbID))

	t.Log("Shim should be shutdown")
	_, err = shimCli.Connect(ctx, &apitask.ConnectRequest{})
	require.Error(t, err)
	require.ErrorContains(t, err, "ttrpc: closed")
}
