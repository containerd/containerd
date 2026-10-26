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
	"errors"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/server/podsandbox"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/internal/eventq"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/types/known/anypb"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func newRestoreTransactionTestService(t *testing.T) (*criService, context.Context) {
	t.Helper()
	ctx := namespaces.WithNamespace(t.Context(), "k8s.io")
	bdb, err := bolt.Open(filepath.Join(t.TempDir(), "metadata.db"), 0o600, nil)
	require.NoError(t, err)
	db := metadata.NewDB(bdb, nil, nil)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	require.NoError(t, db.Init(ctx))
	c := newTestCRIService()
	c.config.RootDir = t.TempDir()
	c.containerEventsQ = eventq.New[*runtime.ContainerEventResponse](time.Minute, func(*runtime.ContainerEventResponse) {})
	t.Cleanup(c.containerEventsQ.Shutdown)
	c.client, err = client.New("", client.WithServices(
		client.WithSandboxStore(metadata.NewSandboxStore(db)),
		client.WithContainerStore(metadata.NewContainerStore(db)),
		client.WithLeasesService(metadata.NewLeaseManager(db)),
	))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, c.client.Close()) })
	c.sandboxService = &restoreRemovalSandboxService{}
	return c, ctx
}

type restoreRemovalSandboxService struct {
	sandboxService
	shutdownErr error
	removed     []string
}

func (s *restoreRemovalSandboxService) SandboxStatus(context.Context, string, string, bool) (sandbox.ControllerStatus, error) {
	return sandbox.ControllerStatus{}, errdefs.ErrNotFound
}

func (s *restoreRemovalSandboxService) ShutdownSandbox(_ context.Context, _, id string) error {
	if s.shutdownErr != nil {
		return s.shutdownErr
	}
	s.removed = append(s.removed, id)
	return nil
}

func addRestoreTransactionSandbox(t *testing.T, ctx context.Context, c *criService, id string, request *runtime.RestorePodRequest) sandbox.Sandbox {
	t.Helper()
	if goruntime.GOOS == "windows" {
		request.Config.Windows = &runtime.WindowsPodSandboxConfig{
			SecurityContext: &runtime.WindowsSandboxSecurityContext{HostProcess: true},
		}
	} else {
		request.Config.Linux = &runtime.LinuxPodSandboxConfig{SecurityContext: &runtime.LinuxSandboxSecurityContext{NamespaceOptions: &runtime.NamespaceOption{Network: runtime.NamespaceMode_NODE}}}
	}
	require.True(t, hostNetwork(request.Config))
	meta := sandboxstore.Metadata{ID: id, Name: makeSandboxName(request.GetConfig().GetMetadata()), Config: request.GetConfig()}
	record := sandbox.Sandbox{ID: id, Runtime: sandbox.RuntimeOpts{Name: "io.containerd.runc.v2"}, Sandboxer: "podsandbox"}
	require.NoError(t, record.AddExtension(podsandbox.MetadataKey, &meta))
	require.NoError(t, record.AddExtension(podRestoreTransactionKey, &podRestoreTransaction{Version: 1}))
	record, err := c.client.SandboxStore().Create(ctx, record)
	require.NoError(t, err)
	sb := sandboxstore.NewSandbox(meta, sandboxstore.Status{State: sandboxstore.StateNotReady})
	sb.Sandboxer = record.Sandboxer
	require.NoError(t, c.sandboxStore.Add(sb))
	return record
}

func TestPodRestoreTransactionCommit(t *testing.T) {
	c, ctx := newRestoreTransactionTestService(t)
	addRestoreTransactionSandbox(t, ctx, c, "restored", restoreTransactionRequest(t))

	require.NoError(t, c.commitPodRestore(ctx, "restored"))
	require.NoError(t, c.recoverPodRestores(ctx), "committed restores survive startup recovery")
	record, err := c.client.SandboxStore().Get(ctx, "restored")
	require.NoError(t, err)
	transaction, err := loadPodRestoreTransaction(record)
	require.NoError(t, err)
	assert.True(t, transaction.Committed)
	assert.Empty(t, c.sandboxService.(*restoreRemovalSandboxService).removed)
}

func TestPodRestoreRecoveryRetriesRemoval(t *testing.T) {
	c, ctx := newRestoreTransactionTestService(t)
	request := restoreTransactionRequest(t)
	addRestoreTransactionSandbox(t, ctx, c, "interrupted", request)
	service := c.sandboxService.(*restoreRemovalSandboxService)
	service.shutdownErr = errors.New("injected shutdown failure")
	require.ErrorContains(t, c.recoverPodRestores(ctx), "injected shutdown failure")
	_, err := c.client.SandboxStore().Get(ctx, "interrupted")
	require.NoError(t, err, "failed removal must retain durable restore intent")
	service.shutdownErr = nil
	require.NoError(t, c.recoverPodRestores(ctx))
	require.NoError(t, c.recoverPodRestores(ctx), "repeated recovery must be harmless")
	_, err = c.client.SandboxStore().Get(ctx, "interrupted")
	require.ErrorIs(t, err, errdefs.ErrNotFound)
	assert.Equal(t, []string{"interrupted"}, service.removed)
}

func TestPodRestoreRecoveryKeepsUnloadedResources(t *testing.T) {
	for _, unloaded := range []string{"sandbox", "container"} {
		t.Run(unloaded, func(t *testing.T) {
			c, ctx := newRestoreTransactionTestService(t)
			request := restoreTransactionRequest(t)
			addRestoreTransactionSandbox(t, ctx, c, "interrupted", request)
			if unloaded == "sandbox" {
				c.sandboxStore.Delete("interrupted")
			} else {
				_, err := c.client.ContainerService().Create(ctx, containers.Container{ID: "unloaded", SandboxID: "interrupted", Spec: &anypb.Any{Value: []byte(`{}`)}, Runtime: containers.RuntimeInfo{Name: "io.containerd.runc.v2"}})
				require.NoError(t, err)
			}
			require.ErrorContains(t, c.recoverPodRestores(ctx), "is not loaded")
			_, err := c.client.SandboxStore().Get(ctx, "interrupted")
			require.NoError(t, err, "do not drop ownership of resources missing from the CRI stores")
		})
	}
}

func TestSaveContainerTaskCheckpointCompletesPreparation(t *testing.T) {
	for _, test := range []struct {
		name    string
		image   string
		saveErr error
	}{
		{name: "task checkpoint", image: "localhost/checkpoint:prepared"},
		{name: "controller without a task checkpoint"},
		{name: "status persistence failure", image: "localhost/checkpoint:prepared", saveErr: errors.New("injected status failure")},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := newTestCRIService()
			root := t.TempDir()
			cntr, err := containerstore.NewContainer(
				containerstore.Metadata{ID: "preparing"},
				containerstore.WithStatus(containerstore.Status{CreatedAt: 1, RestorePending: true}, root),
			)
			require.NoError(t, err)
			if test.saveErr != nil {
				cntr.Status = &checkpointSyncFailureStatus{StatusStorage: cntr.Status, err: test.saveErr}
			}
			require.NoError(t, c.containerStore.Add(cntr))
			require.ErrorIs(t, setContainerStarting(cntr), errdefs.ErrFailedPrecondition)

			err = c.saveContainerTaskCheckpoint(cntr.ID, test.image)
			if test.saveErr != nil {
				require.ErrorIs(t, err, test.saveErr)
			} else {
				require.NoError(t, err)
			}
			status, err := containerstore.LoadStatus(root, cntr.ID)
			require.NoError(t, err)
			assert.Equal(t, cntr.Status.Get(), status, "checkpoint ownership and preparation must be saved together")
			assert.Equal(t, test.saveErr != nil, status.RestorePending)
			if test.saveErr != nil {
				assert.Empty(t, status.TaskCheckpointImage)
				require.ErrorIs(t, setContainerStarting(cntr), errdefs.ErrFailedPrecondition)
			} else {
				assert.Equal(t, test.image, status.TaskCheckpointImage)
				require.NoError(t, setContainerStarting(cntr))
			}
		})
	}
}

func TestPodRestoreRecoveryRejectsUnknownTransactionVersion(t *testing.T) {
	c, ctx := newRestoreTransactionTestService(t)
	record := addRestoreTransactionSandbox(t, ctx, c, "restored", restoreTransactionRequest(t))
	require.NoError(t, record.AddExtension(podRestoreTransactionKey, &podRestoreTransaction{Version: 2}))
	_, err := c.client.SandboxStore().Update(ctx, record, "extensions."+podRestoreTransactionKey)
	require.NoError(t, err)

	require.ErrorIs(t, c.recoverPodRestores(ctx), errdefs.ErrFailedPrecondition)
	_, err = c.client.SandboxStore().Get(ctx, record.ID)
	require.NoError(t, err)
	assert.Empty(t, c.sandboxService.(*restoreRemovalSandboxService).removed)
}
