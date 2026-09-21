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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/core/sandbox"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

// statusSandboxService answers SandboxStatus per sandbox id and records the
// sandboxer each query was addressed to.
type statusSandboxService struct {
	fakeSandboxService
	statuses   map[string]sandbox.ControllerStatus
	errs       map[string]error
	sandboxers map[string]string
}

func (s *statusSandboxService) SandboxStatus(_ context.Context, sandboxer, id string, _ bool) (sandbox.ControllerStatus, error) {
	s.sandboxers[id] = sandboxer
	if err, ok := s.errs[id]; ok {
		return sandbox.ControllerStatus{}, err
	}
	return s.statuses[id], nil
}

func sandboxRecord(t *testing.T, id, sandboxer string, hostNetwork bool) sandbox.Sandbox {
	t.Helper()
	network := runtime.NamespaceMode_POD
	if hostNetwork {
		network = runtime.NamespaceMode_NODE
	}
	record := sandbox.Sandbox{
		ID:        id,
		Sandboxer: sandboxer,
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	require.NoError(t, record.AddExtension(sandboxstore.MetadataKey, &sandboxstore.Metadata{
		ID:        id,
		Name:      "name-" + id,
		NetNSPath: "/var/run/netns/" + id,
		Config: &runtime.PodSandboxConfig{
			Metadata: &runtime.PodSandboxMetadata{Name: id, Namespace: "default", Uid: "uid-" + id},
			Linux: &runtime.LinuxPodSandboxConfig{
				SecurityContext: &runtime.LinuxSandboxSecurityContext{
					NamespaceOptions: &runtime.NamespaceOption{Network: network},
				},
			},
			// Host networking is expressed per platform: the namespace mode on
			// Linux, a host process pod on Windows.
			Windows: &runtime.WindowsPodSandboxConfig{
				SecurityContext: &runtime.WindowsSandboxSecurityContext{HostProcess: hostNetwork},
			},
		},
	}))
	return record
}

func TestRecoverSandboxes(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	exitedAt := createdAt.Add(time.Hour)

	svc := &statusSandboxService{
		statuses: map[string]sandbox.ControllerStatus{
			"ready": {
				SandboxID: "ready",
				Pid:       4242,
				State:     runtime.PodSandboxState_SANDBOX_READY.String(),
				CreatedAt: createdAt,
				Address:   "/run/shim/ready.sock",
				Version:   3,
			},
			"stopped": {
				SandboxID: "stopped",
				Pid:       0,
				State:     runtime.PodSandboxState_SANDBOX_NOTREADY.String(),
				CreatedAt: createdAt,
				ExitedAt:  exitedAt,
			},
			"odd-state": {
				SandboxID: "odd-state",
				State:     "SANDBOX_UNKNOWN",
			},
		},
		errs: map[string]error{
			"gone":   errdefs.ErrNotFound,
			"broken": errors.New("controller is unhappy"),
		},
		sandboxers: map[string]string{},
	}
	c := newTestCRIService()
	c.sandboxService = svc
	// A second record with the name of "ready".
	sameName := sandboxRecord(t, "same-name", "shim", true)
	var sameNameMeta sandboxstore.Metadata
	require.NoError(t, sameName.GetExtension(sandboxstore.MetadataKey, &sameNameMeta))
	sameNameMeta.Name = "name-ready"
	require.NoError(t, sameName.AddExtension(sandboxstore.MetadataKey, &sameNameMeta))

	// A record written by 2.3 or 2.4 carries the SELinux label of its pause
	// sandbox as a controller label.
	labeled := sandboxRecord(t, "labeled", "podsandbox", true)
	labeled.Labels = map[string]string{"selinux_label": "system_u:system_r:container_t:s0:c1,c2"}

	records := []sandbox.Sandbox{
		sandboxRecord(t, "ready", "shim", false),
		sandboxRecord(t, "stopped", "podsandbox", true),
		sandboxRecord(t, "odd-state", "shim", true),
		sandboxRecord(t, "gone", "shim", true),
		sandboxRecord(t, "broken", "remote", true),
		// A record without CRI metadata cannot be represented to kubelet.
		{ID: "no-metadata", Sandboxer: "shim"},
		sameName,
		// The cache cannot index an id with a space.
		sandboxRecord(t, "bad id", "shim", true),
		labeled,
	}
	// The updated resources of a sandbox are stored as their own extension.
	require.NoError(t, records[0].AddExtension(sandboxstore.UpdatedResourcesKey, &sandboxstore.UpdatedResources{
		Resources: &runtime.LinuxContainerResources{MemoryLimitInBytes: 5},
		Overhead:  &runtime.LinuxContainerResources{MemoryLimitInBytes: 10},
	}))

	c.recoverSandboxes(ctx, records)

	// Every controller is asked about its own sandboxes.
	assert.Equal(t, map[string]string{
		"ready":     "shim",
		"stopped":   "podsandbox",
		"odd-state": "shim",
		"gone":      "shim",
		"broken":    "remote",
		"same-name": "shim",
		"bad id":    "shim",
		"labeled":   "podsandbox",
	}, svc.sandboxers)

	// A ready sandbox keeps what the controller reported.
	ready, err := c.sandboxStore.Get("ready")
	require.NoError(t, err)
	status := ready.Status.Get()
	assert.Equal(t, sandboxstore.StateReady, status.State)
	assert.EqualValues(t, 4242, status.Pid)
	assert.Equal(t, createdAt, status.CreatedAt)
	assert.Equal(t, sandboxstore.Endpoint{Address: "/run/shim/ready.sock", Version: 3}, ready.Endpoint)
	assert.Equal(t, "shim", ready.Sandboxer)
	assert.Equal(t, "name-ready", ready.Name)
	require.NotNil(t, status.Resources)
	assert.EqualValues(t, 5, status.Resources.GetLinux().GetMemoryLimitInBytes())
	require.NotNil(t, status.Overhead)
	assert.EqualValues(t, 10, status.Overhead.GetLinux().GetMemoryLimitInBytes())

	// A stopped sandbox is not ready, its pid is zero and it keeps its exit
	// time.
	stopped, err := c.sandboxStore.Get("stopped")
	require.NoError(t, err)
	status = stopped.Status.Get()
	assert.Equal(t, sandboxstore.StateNotReady, status.State)
	assert.Zero(t, status.Pid)
	assert.Equal(t, exitedAt, status.ExitedAt)

	// A sandbox that is not on the host network gets its network namespace
	// handle back. hostNetwork decides per platform which sandboxes are on the
	// host network.
	for _, sb := range []sandboxstore.Sandbox{ready, stopped} {
		if hostNetwork(sb.Config) {
			assert.Nil(t, sb.NetNS, "no network namespace handle for the host network sandbox %s", sb.ID)
		} else {
			assert.NotNil(t, sb.NetNS, "sandbox %s should get its network namespace handle back", sb.ID)
		}
	}

	// A state the server does not know stays unknown.
	odd, err := c.sandboxStore.Get("odd-state")
	require.NoError(t, err)
	assert.Equal(t, sandboxstore.StateUnknown, odd.Status.Get().State)

	// A controller that does not have an instance for the sandbox reports it
	// not ready.
	gone, err := c.sandboxStore.Get("gone")
	require.NoError(t, err)
	assert.Equal(t, sandboxstore.StateNotReady, gone.Status.Get().State)

	// A controller that cannot answer leaves the state unknown, with the
	// record's creation time.
	broken, err := c.sandboxStore.Get("broken")
	require.NoError(t, err)
	assert.Equal(t, sandboxstore.StateUnknown, broken.Status.Get().State)
	assert.Equal(t, records[4].CreatedAt, broken.Status.Get().CreatedAt)

	// Every recovered sandbox has its name reserved.
	for _, id := range []string{"ready", "stopped", "odd-state", "gone", "broken"} {
		err := c.sandboxNameIndex.Reserve("name-"+id, "another-id")
		assert.Error(t, err, "name of %s should be reserved", id)
	}

	// A second stored sandbox with the name of another one is served too.
	// Kubelet sees both and removes the one it does not want.
	sameNameSb, err := c.sandboxStore.Get("same-name")
	require.NoError(t, err)
	assert.Equal(t, "name-ready", sameNameSb.Name)

	// A record the cache cannot index is skipped. The other records are
	// unaffected.
	_, err = c.sandboxStore.Get("bad id")
	assert.True(t, errdefs.IsNotFound(err))
	assert.NoError(t, c.sandboxNameIndex.Reserve("name-bad id", "another-id"), "the name of a skipped sandbox is not reserved")

	// The process label persisted as a controller label is restored.
	labeledSb, err := c.sandboxStore.Get("labeled")
	require.NoError(t, err)
	assert.Equal(t, "system_u:system_r:container_t:s0:c1,c2", labeledSb.ProcessLabel)

	// The record without metadata is skipped.
	_, err = c.sandboxStore.Get("no-metadata")
	assert.True(t, errdefs.IsNotFound(err))
}

func TestCleanupOrphanedIDDirs(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	for _, dir := range []string{"live", "orphan"} {
		require.NoError(t, os.Mkdir(filepath.Join(base, dir), 0o755))
	}
	// A file is not an id directory and is left alone.
	require.NoError(t, os.WriteFile(filepath.Join(base, "stray"), nil, 0o644))

	require.NoError(t, cleanupOrphanedIDDirs(ctx, map[string]struct{}{"live": {}}, base))

	assert.DirExists(t, filepath.Join(base, "live"), "a directory with a live id is kept")
	assert.NoDirExists(t, filepath.Join(base, "orphan"), "a directory without a live id is removed")
	assert.FileExists(t, filepath.Join(base, "stray"))

	// A missing base directory is not an error.
	require.NoError(t, cleanupOrphanedIDDirs(ctx, nil, filepath.Join(base, "missing")))
}
