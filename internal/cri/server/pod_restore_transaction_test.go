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
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/sandbox"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
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
	require.NoError(t, c.recoverPodRestores(ctx), "one interrupted restore must not block startup")
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
			require.NoError(t, c.recoverPodRestores(ctx), "unloaded restore resources must not block startup")
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

func TestPodRestoreRecoveryKeepsUnknownTransactionVersion(t *testing.T) {
	c, ctx := newRestoreTransactionTestService(t)
	record := addRestoreTransactionSandbox(t, ctx, c, "restored", restoreTransactionRequest(t))
	require.NoError(t, record.AddExtension(podRestoreTransactionKey, &podRestoreTransaction{Version: 2}))
	_, err := c.client.SandboxStore().Update(ctx, record, "extensions."+podRestoreTransactionKey)
	require.NoError(t, err)

	require.NoError(t, c.recoverPodRestores(ctx), "an unsupported restore transaction must not block startup")
	record, err = c.client.SandboxStore().Get(ctx, record.ID)
	require.NoError(t, err)
	_, err = loadPodRestoreTransaction(record)
	require.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
	assert.Empty(t, c.sandboxService.(*restoreRemovalSandboxService).removed)
}

type restoreCleanupLeaseManager struct {
	leases.Manager
	deleteErr error
}

func (m *restoreCleanupLeaseManager) Delete(ctx context.Context, lease leases.Lease, opts ...leases.DeleteOpt) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	return m.Manager.Delete(ctx, lease, opts...)
}

type restoreCreateFailureSandboxService struct {
	restoreRemovalSandboxService
}

func (s *restoreCreateFailureSandboxService) CreateSandbox(context.Context, sandbox.Sandbox, ...sandbox.CreateOpt) error {
	return errors.New("injected sandbox creation failure")
}

func (s *restoreCreateFailureSandboxService) StopSandbox(context.Context, string, string, ...sandbox.StopOpt) error {
	return errdefs.ErrNotFound
}

func TestPodRestoreRetainsSandboxAfterLeaseCleanupFailure(t *testing.T) {
	c, ctx := newRestoreTransactionTestService(t)
	leaseManager := &restoreCleanupLeaseManager{Manager: c.client.LeasesService(), deleteErr: errors.New("injected lease cleanup failure")}
	testClient, err := client.New("", client.WithServices(
		client.WithSandboxStore(c.client.SandboxStore()),
		client.WithContainerStore(c.client.ContainerService()),
		client.WithLeasesService(leaseManager),
	))
	require.NoError(t, err)
	originalClient := c.client
	t.Cleanup(func() { assert.NoError(t, originalClient.Close()) })
	c.client = testClient
	c.sandboxService = &restoreCreateFailureSandboxService{}
	c.config.Runtimes = map[string]criconfig.Runtime{"restore": {Type: "io.containerd.runc.v2", Sandboxer: "podsandbox"}}
	config := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{Name: "restore", Namespace: "default", Uid: "restore-uid"},
		Linux:    &runtime.LinuxPodSandboxConfig{SecurityContext: &runtime.LinuxSandboxSecurityContext{NamespaceOptions: &runtime.NamespaceOption{Network: runtime.NamespaceMode_NODE}}},
		Windows:  &runtime.WindowsPodSandboxConfig{SecurityContext: &runtime.WindowsSandboxSecurityContext{HostProcess: true}},
	}
	_, err = c.runPodSandboxForRestore(ctx, &runtime.RunPodSandboxRequest{Config: config, RuntimeHandler: "restore"})
	require.ErrorContains(t, err, "injected sandbox creation failure")
	records, err := c.client.SandboxStore().List(ctx)
	require.NoError(t, err)
	require.Len(t, records, 1, "retain the restore transaction while lease cleanup needs retry")
	id := records[0].ID
	_, err = c.sandboxStore.Get(id)
	require.NoError(t, err, "retained restore must remain reachable through CRI for cleanup")
	require.Error(t, c.sandboxNameIndex.Reserve(makeSandboxName(config.Metadata), "other"), "the retained sandbox must continue to own its name")

	leaseManager.deleteErr = nil
	require.NoError(t, c.recoverPodRestores(ctx))
	_, err = c.client.SandboxStore().Get(ctx, id)
	require.ErrorIs(t, err, errdefs.ErrNotFound)
	retainedLeases, err := leaseManager.List(ctx)
	require.NoError(t, err)
	require.Empty(t, retainedLeases)
}

func TestPodRestoreRecoveryContinuesAfterUnloadedSandbox(t *testing.T) {
	c, ctx := newRestoreTransactionTestService(t)
	addRestoreTransactionSandbox(t, ctx, c, "first-unloaded", restoreTransactionRequest(t))
	c.sandboxStore.Delete("first-unloaded")
	addRestoreTransactionSandbox(t, ctx, c, "second-interrupted", restoreTransactionRequest(t))

	require.NoError(t, c.recoverPodRestores(ctx))
	_, err := c.client.SandboxStore().Get(ctx, "first-unloaded")
	require.NoError(t, err, "retain the failed transaction for a later cleanup attempt")
	_, err = c.client.SandboxStore().Get(ctx, "second-interrupted")
	require.ErrorIs(t, err, errdefs.ErrNotFound, "failure of one restore must not prevent cleanup of another")
}

func TestPodRestoreRecoveryKeepsStartBlocked(t *testing.T) {
	for _, transaction := range []struct {
		name      string
		version   int
		committed bool
	}{
		{name: "uncommitted", version: 1},
		{name: "unknown version", version: 2},
		{name: "committed", version: 1, committed: true},
	} {
		t.Run(transaction.name, func(t *testing.T) {
			c, ctx := newRestoreTransactionTestService(t)
			record := addRestoreTransactionSandbox(t, ctx, c, "restored", restoreTransactionRequest(t))
			require.NoError(t, record.AddExtension(podRestoreTransactionKey, &podRestoreTransaction{Version: transaction.version, Committed: transaction.committed}))
			_, err := c.client.SandboxStore().Update(ctx, record, "extensions."+podRestoreTransactionKey)
			require.NoError(t, err)
			container, err := containerstore.NewContainer(containerstore.Metadata{ID: "prepared", SandboxID: record.ID},
				containerstore.WithStatus(containerstore.Status{CreatedAt: 1, TaskCheckpointImage: "prepared-checkpoint"}, t.TempDir()))
			require.NoError(t, err)
			require.NoError(t, c.containerStore.Add(container))
			_, err = c.client.ContainerService().Create(ctx, containers.Container{ID: "unloaded", SandboxID: record.ID, Spec: &anypb.Any{Value: []byte(`{}`)}, Runtime: containers.RuntimeInfo{Name: "io.containerd.runc.v2"}})
			require.NoError(t, err)

			require.NoError(t, c.recoverPodRestores(ctx))
			if transaction.committed {
				require.NoError(t, setContainerStarting(container))
			} else {
				require.ErrorIs(t, setContainerStarting(container), errdefs.ErrFailedPrecondition)
			}
			_, err = c.client.SandboxStore().Get(ctx, record.ID)
			require.NoError(t, err)
		})
	}
}
