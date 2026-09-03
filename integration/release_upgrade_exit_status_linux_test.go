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
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/continuity/fs"
	"github.com/containerd/errdefs"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/testing/protocmp"

	eventtypes "github.com/containerd/containerd/api/events"
	containerd "github.com/containerd/containerd/v2/client"
	ctrdruntime "github.com/containerd/containerd/v2/core/runtime"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/integration/remote"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/containerd/v2/plugins"
)

func shouldHandleShimExitStatusAfterUpgrade(previousReleaseBinDir string) setupUpgradeVerifyCase {
	const (
		shimExitStatus          uint32 = 42
		shimExitStatusNamespace        = "kill-shim-before-delete-task"
	)

	return func(t *testing.T, _ int, rSvc *remote.RuntimeService, _ *remote.ImageService) ([]upgradeVerifyCaseFunc, beforeUpgradeHookFunc) {
		client := newUpgradeContainerdClient(t, rSvc, shimExitStatusNamespace)
		ctx := t.Context()

		image, err := client.Pull(ctx, images.Get(images.BusyBox), containerd.WithPullUnpack)
		require.NoError(t, err)

		const id = "old-shim-exit-status"
		// previousReleaseBinDir is shared by all upgrade cases, so use a private
		// copy that can be replaced without affecting later tests.
		oldShim := filepath.Join(t.TempDir(), "containerd-shim-runc-v2")

		require.NoError(t, fs.CopyFile(oldShim,
			filepath.Join(previousReleaseBinDir, "bin", "containerd-shim-runc-v2")))
		require.NoError(t, os.Chmod(oldShim, 0755))

		container, err := client.NewContainer(ctx, id,
			containerd.WithNewSnapshot(id, image),
			containerd.WithNewSpec(oci.WithImageConfig(image),
				oci.WithProcessArgs("sh", "-c", fmt.Sprintf("exit %d", shimExitStatus))),
			containerd.WithRuntime(plugins.RuntimeRuncV2, nil),
		)
		require.NoError(t, err)

		_, err = container.NewTask(ctx, cio.NullIO, containerd.WithRuntimePath(oldShim))
		require.NoError(t, err)

		beforeUpgrade := func(t *testing.T) {
			// Replace the shim binary to verify that the current shim can delete an old bundle.
			require.NoError(t, os.Remove(oldShim))
			require.NoError(t, fs.CopyFile(oldShim,
				filepath.Join(*buildDir, "containerd-shim-runc-v2")))
			require.NoError(t, os.Chmod(oldShim, 0755))
		}

		verify := func(t *testing.T, rSvc *remote.RuntimeService, _ *remote.ImageService) {
			client := newUpgradeContainerdClient(t, rSvc, shimExitStatusNamespace)
			ctx := t.Context()

			container, err := client.LoadContainer(ctx, id)
			require.NoError(t, err)
			task, err := container.Task(ctx, nil)
			require.NoError(t, err)
			taskPID := task.Pid()
			require.NotZero(t, taskPID)

			eventCh, eventErrCh := client.EventService().Subscribe(ctx,
				fmt.Sprintf(`topic=="%s",event.container_id=="%s"`, ctrdruntime.TaskExitEventTopic, id),
				fmt.Sprintf(`topic=="%s",event.container_id=="%s"`, ctrdruntime.TaskDeleteEventTopic, id),
			)

			shim := buildShimClientFromNamespacedBundle(t, rSvc, shimExitStatusNamespace, id)
			shimProcess, err := os.FindProcess(int(shimPid(ctx, t, shim.cli)))
			require.NoError(t, err)
			defer shimProcess.Release()
			defer shimProcess.Signal(syscall.SIGKILL)

			require.NoError(t, task.Start(ctx))

			gotEvents := waitForTaskExitDeleteEvents(t, id, eventCh, eventErrCh, 1, 30*time.Second)
			wantEvents := []taskEvent{{Exit: &eventtypes.TaskExit{
				ContainerID: id,
				ID:          id,
				Pid:         taskPID,
				ExitStatus:  shimExitStatus,
			}}}
			cmpOpts := []cmp.Option{
				protocmp.Transform(),
				protocmp.IgnoreFields(&eventtypes.TaskExit{}, "exited_at"),
				protocmp.IgnoreFields(&eventtypes.TaskDelete{}, "exited_at"),
			}
			if diff := cmp.Diff(wantEvents, gotEvents, cmpOpts...); diff != "" {
				t.Fatalf("unexpected task exit event (-want +got):\n%s", diff)
			}

			// We should have two events after killed shim
			require.NoError(t, shimProcess.Signal(syscall.SIGKILL))
			gotEvents = waitForTaskExitDeleteEvents(t, id, eventCh, eventErrCh, 2, 30*time.Second)
			wantEvents = []taskEvent{
				{Exit: &eventtypes.TaskExit{
					ContainerID: id,
					ID:          id,
					Pid:         taskPID,
					ExitStatus:  uint32(128 + syscall.SIGKILL),
				}},
				{Delete: &eventtypes.TaskDelete{
					ContainerID: id,
					Pid:         taskPID,
					ExitStatus:  uint32(128 + syscall.SIGKILL),
				}},
			}
			if diff := cmp.Diff(wantEvents, gotEvents, cmpOpts...); diff != "" {
				t.Fatalf("unexpected task cleanup events (-want +got):\n%s", diff)
			}

			require.NoError(t, Eventually(func() (bool, error) {
				_, err := container.Task(ctx, nil)
				if err == nil {
					return false, nil
				}
				if errdefs.IsNotFound(err) {
					return true, nil
				}
				return false, err
			}, 100*time.Millisecond, 10*time.Second))
			require.NoError(t, container.Delete(ctx, containerd.WithSnapshotCleanup))
		}
		return []upgradeVerifyCaseFunc{verify}, beforeUpgrade
	}
}

func newUpgradeContainerdClient(t *testing.T, rSvc *remote.RuntimeService, namespace string) *containerd.Client {
	t.Helper()

	endpoint := criRuntimeInfo(t, rSvc)["containerdEndpoint"].(string)
	client, err := containerd.New(
		strings.TrimPrefix(endpoint, "unix://"),
		containerd.WithDefaultNamespace(namespace))
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return client
}
