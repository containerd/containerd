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
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/testing/protocmp"

	eventtypes "github.com/containerd/containerd/api/events"
	apitask "github.com/containerd/containerd/api/runtime/task/v3"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/events"
	ctrdruntime "github.com/containerd/containerd/v2/core/runtime"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
	ptypes "github.com/containerd/containerd/v2/pkg/protobuf/types"
	"github.com/containerd/containerd/v2/plugins"
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

	container, err := containerdClient.LoadContainer(ctx, sbID)
	require.NoError(t, err)
	task, err := container.Task(ctx, nil)
	require.NoError(t, err)
	sandboxPID := task.Pid()
	require.NotZero(t, sandboxPID)

	shimCli := connectToShim(ctx, t, containerdEndpoint, 3, sbID)
	shimPID := shimPid(ctx, t, shimCli)

	eventCh, eventErrCh := containerdClient.EventService().Subscribe(ctx,
		fmt.Sprintf(`topic=="%s",event.container_id=="%s"`, ctrdruntime.TaskExitEventTopic, sbID),
		fmt.Sprintf(`topic=="%s",event.container_id=="%s"`, ctrdruntime.TaskDeleteEventTopic, sbID),
	)

	require.NoError(t, syscall.Kill(int(shimPID), syscall.SIGKILL))
	gotEvents := waitForTaskExitDeleteEvents(t, sbID, eventCh, eventErrCh, 2, 10*time.Second)
	wantEvents := []taskEvent{
		{Exit: &eventtypes.TaskExit{
			ContainerID: sbID,
			ID:          sbID,
			Pid:         sandboxPID,
			ExitStatus:  uint32(128 + syscall.SIGKILL),
		}},
		{Delete: &eventtypes.TaskDelete{
			ContainerID: sbID,
			Pid:         sandboxPID,
			ExitStatus:  uint32(128 + syscall.SIGKILL),
		}},
	}
	if diff := cmp.Diff(wantEvents, gotEvents,
		protocmp.Transform(),
		protocmp.IgnoreFields(&eventtypes.TaskExit{}, "exited_at"),
		protocmp.IgnoreFields(&eventtypes.TaskDelete{}, "exited_at"),
	); diff != "" {
		t.Fatalf("unexpected task events (-want +got):\n%s", diff)
	}
}

// Regression test for https://github.com/containerd/containerd/issues/13293 (orphaned shim state after shim SIGKILL).
// This test asserts that containerd publishes task exit and delete events for a container when the shim disconnects.
func TestKillShimPublishesTaskExitAndDeleteEventsForContainer(t *testing.T) {
	ctx := namespaces.WithNamespace(t.Context(), "k8s.io")

	sbConfig := PodSandboxConfig("sandbox", t.Name(), WithHostNetwork)
	sbID, err := runtimeService.RunPodSandbox(sbConfig, "")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, runtimeService.StopPodSandbox(sbID))
		require.NoError(t, runtimeService.RemovePodSandbox(sbID))
	})
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

	container, err := containerdClient.LoadContainer(ctx, cnID)
	require.NoError(t, err)
	task, err := container.Task(ctx, nil)
	require.NoError(t, err)
	workloadPID := task.Pid()
	require.NotZero(t, workloadPID)

	shimCli := connectToShim(ctx, t, containerdEndpoint, 3, sbID)
	shimPID := shimPid(ctx, t, shimCli)

	eventCh, eventErrCh := containerdClient.EventService().Subscribe(ctx,
		fmt.Sprintf(`topic=="%s",event.container_id=="%s"`, ctrdruntime.TaskExitEventTopic, cnID),
		fmt.Sprintf(`topic=="%s",event.container_id=="%s"`, ctrdruntime.TaskDeleteEventTopic, cnID),
	)

	require.NoError(t, syscall.Kill(int(shimPID), syscall.SIGKILL))
	gotEvents := waitForTaskExitDeleteEvents(t, cnID, eventCh, eventErrCh, 2, 10*time.Second)

	wantEvents := []taskEvent{
		{Exit: &eventtypes.TaskExit{
			ContainerID: cnID,
			ID:          cnID,
			Pid:         workloadPID,
			ExitStatus:  uint32(128 + syscall.SIGKILL),
		}},
		{Delete: &eventtypes.TaskDelete{
			ContainerID: cnID,
			Pid:         workloadPID,
			ExitStatus:  uint32(128 + syscall.SIGKILL),
		}},
	}
	if diff := cmp.Diff(wantEvents, gotEvents,
		protocmp.Transform(),
		protocmp.IgnoreFields(&eventtypes.TaskExit{}, "exited_at"),
		protocmp.IgnoreFields(&eventtypes.TaskDelete{}, "exited_at"),
	); diff != "" {
		t.Fatalf("unexpected task events (-want +got):\n%s", diff)
	}

	// Wait for the container to exit and ensure it is fully cleaned up.
	require.NoError(t, Eventually(func() (bool, error) {
		s, err := runtimeService.ContainerStatus(cnID)
		if err != nil {
			return false, err
		}
		if s.State == runtime.ContainerState_CONTAINER_EXITED {
			return true, nil
		}
		return false, nil
	}, 1000*time.Millisecond, 30*time.Second))
}

func TestKillShimPreservesExitStatusForMultipleContainers(t *testing.T) {
	const groupAnnotation = "io.containerd.runc.v2.group"

	ctx := namespaces.WithNamespace(t.Context(), namespaces.Default)
	cleanupCtx := namespaces.WithNamespace(context.Background(), namespaces.Default)
	shimPath := filepath.Join(*buildDir, "containerd-shim-runc-v2")
	require.FileExists(t, shimPath)

	image, err := containerdClient.Pull(ctx, images.Get(images.BusyBox), containerd.WithPullUnpack)
	require.NoError(t, err)

	type testTask struct {
		id         string
		exitStatus uint32
		task       containerd.Task
		eventCh    <-chan *events.Envelope
		eventErrCh <-chan error
	}
	groupID := t.Name()
	newTask := func(id string, exitStatus uint32) testTask {
		container, err := containerdClient.NewContainer(ctx, id,
			containerd.WithNewSnapshot(id, image),
			containerd.WithNewSpec(
				oci.WithImageConfig(image),
				oci.WithProcessArgs("sh", "-c", fmt.Sprintf("exit %d", exitStatus)),
				oci.WithAnnotations(map[string]string{groupAnnotation: groupID}),
			),
			containerd.WithRuntime(plugins.RuntimeRuncV2, nil),
		)
		require.NoError(t, err)
		t.Cleanup(func() {
			_ = container.Delete(cleanupCtx, containerd.WithSnapshotCleanup)
		})

		t.Logf("Use the shim built from the current source: %s", shimPath)
		task, err := container.NewTask(ctx, cio.NullIO, containerd.WithRuntimePath(shimPath))
		require.NoError(t, err)
		require.NotZero(t, task.Pid())
		t.Cleanup(func() {
			_, _ = task.Delete(cleanupCtx, containerd.WithProcessKill)
		})

		eventCh, eventErrCh := containerdClient.EventService().Subscribe(ctx,
			fmt.Sprintf(`topic=="%s",event.container_id=="%s"`, ctrdruntime.TaskExitEventTopic, id),
			fmt.Sprintf(`topic=="%s",event.container_id=="%s"`, ctrdruntime.TaskDeleteEventTopic, id),
		)
		return testTask{id, exitStatus, task, eventCh, eventErrCh}
	}
	tasks := []testTask{
		newTask("multi-container-exit-42", 42),
		newTask("multi-container-exit-43", 43),
	}

	t.Log("Start two containers in the same shim with different exit statuses")
	shimClient := connectToShim(ctx, t, containerdEndpoint, 3, groupID)
	for _, tc := range tasks {
		require.NoError(t, tc.task.Start(ctx))
	}

	checkEvents := func(tc testTask, afterShimExit bool) {
		wantEvents := []taskEvent{{Exit: &eventtypes.TaskExit{
			ContainerID: tc.id,
			ID:          tc.id,
			Pid:         tc.task.Pid(),
			ExitStatus:  tc.exitStatus,
		}}}
		if afterShimExit {
			wantEvents = append(wantEvents, taskEvent{Delete: &eventtypes.TaskDelete{
				ContainerID: tc.id,
				Pid:         tc.task.Pid(),
				ExitStatus:  tc.exitStatus,
			}})
		}

		gotEvents := waitForTaskExitDeleteEvents(t, tc.id, tc.eventCh, tc.eventErrCh, len(wantEvents), 10*time.Second)

		if diff := cmp.Diff(wantEvents, gotEvents,
			protocmp.Transform(),
			protocmp.IgnoreFields(&eventtypes.TaskExit{}, "exited_at"),
			protocmp.IgnoreFields(&eventtypes.TaskDelete{}, "exited_at"),
		); diff != "" {
			t.Fatalf("unexpected task events for %q (-want +got):\n%s", tc.id, diff)
		}
	}
	for _, tc := range tasks {
		checkEvents(tc, false)
	}

	t.Log("Kill the shared shim after both containers have exited")
	shimProcess, err := os.FindProcess(int(shimPid(ctx, t, shimClient)))
	require.NoError(t, err)
	defer shimProcess.Release()

	t.Log("Verify dead-shim cleanup preserves each container's exit status")
	require.NoError(t, shimProcess.Signal(syscall.SIGKILL))
	for _, tc := range tasks {
		checkEvents(tc, true)
	}
}

// taskEvent is either a task exit or delete event.
type taskEvent struct {
	Exit   *eventtypes.TaskExit
	Delete *eventtypes.TaskDelete
}

func waitForTaskExitDeleteEvents(t *testing.T, id string,
	ch <-chan *events.Envelope, errCh <-chan error, eventCount int, timeout time.Duration,
) []taskEvent {
	t.Helper()

	received := make([]taskEvent, 0, eventCount)
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for len(received) < eventCount {
		select {
		case <-timer.C:
			t.Fatalf("timed out waiting for %d task exit/delete events for %q", eventCount, id)
		case err, ok := <-errCh:
			if !ok {
				t.Fatalf("event subscription closed while waiting for %q", id)
			}
			require.NoError(t, err, "event subscription failed")
		case env, ok := <-ch:
			require.True(t, ok, "event subscription closed while waiting for %q", id)
			t.Logf("received event %q for %q", env.Topic, id)

			event, err := typeurl.UnmarshalAny(env.Event)
			require.NoError(t, err, "failed to unmarshal event")

			switch event := event.(type) {
			case *eventtypes.TaskExit:
				require.Equal(t, id, event.ContainerID)
				require.NoError(t, event.GetExitedAt().CheckValid())
				received = append(received, taskEvent{Exit: event})
			case *eventtypes.TaskDelete:
				require.Equal(t, id, event.ContainerID)
				require.NoError(t, event.GetExitedAt().CheckValid())
				received = append(received, taskEvent{Delete: event})
			default:
				t.Fatalf("unexpected task event type %T", event)
			}
		}
	}

	return received
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
