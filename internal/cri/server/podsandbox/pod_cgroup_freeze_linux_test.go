//go:build linux

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

package podsandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidatePodCgroupPath(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		path    string
		wantErr string
	}{
		{name: "empty", wantErr: "sandbox config has none"},
		{name: "relative", path: "kubepods/pod123", wantErr: "must be an absolute path"},
		{name: "unclean", path: "/kubepods/../pod123", wantErr: "must be a clean path"},
		{name: "trailing separator", path: "/kubepods/pod123/", wantErr: "must be a clean path"},
		{name: "root", path: "/", wantErr: "refusing to freeze the root cgroup"},
		{name: "mountpoint", path: "/sys/fs/cgroup", wantErr: "must be relative to the cgroup filesystem mountpoint"},
		{name: "path below mountpoint", path: "/sys/fs/cgroup/kubepods/pod123", wantErr: "must be relative to the cgroup filesystem mountpoint"},
		{name: "cgroupfs", path: "/kubepods/burstable/pod123"},
		{name: "systemd", path: "/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod123.slice"},
		{name: "systemd slice name", path: "kubepods-burstable-pod123.slice"},
		{name: "systemd root slice", path: "-.slice", wantErr: "refusing to freeze the root systemd slice"},
		{name: "systemd empty slice component", path: "/kubepods--pod123.slice", wantErr: "invalid systemd slice name"},
		{name: "systemd leading dash", path: "/-kubepods.slice", wantErr: "invalid systemd slice name"},
		{name: "systemd suffix only", path: "/.slice", wantErr: "invalid systemd slice name"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := validatePodCgroupPath(test.path)
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestPodCgroupFilesystemPath(t *testing.T) {
	for _, test := range []struct {
		parent string
		want   string
	}{
		{parent: "/kubepods/burstable/pod123", want: "/kubepods/burstable/pod123"},
		// The kubelet passes the expanded systemd path.
		{parent: "/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod123.slice", want: "/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod123.slice"},
		// runc places containers by the last slice name only.
		{parent: "kubepods-burstable-pod123.slice", want: "/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod123.slice"},
		{parent: "/containerd-test.slice", want: "/containerd.slice/containerd-test.slice"},
		{parent: "/pod123.slice", want: "/pod123.slice"},
	} {
		got, err := podCgroupFilesystemPath(test.parent)
		require.NoError(t, err, test.parent)
		assert.Equal(t, test.want, got, test.parent)
	}
}

func TestDirectPodCgroupFreezerV1(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "freezer.state")
	require.NoError(t, os.WriteFile(statePath, []byte("THAWED"), 0o600))
	freezer := &directPodCgroupFreezer{controlPath: statePath, statePath: statePath, version: 1}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	requested, err := freezer.Freeze(ctx)
	require.NoError(t, err)
	require.True(t, requested)
	state, err := freezer.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, podCgroupStateFrozen, state)
	require.NoError(t, freezer.Thaw(ctx))
	state, err = freezer.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, podCgroupStateThawed, state)
}

func TestDirectPodCgroupFreezerV2(t *testing.T) {
	dir := t.TempDir()
	controlPath := filepath.Join(dir, "cgroup.freeze")
	eventsPath := filepath.Join(dir, "cgroup.events")
	require.NoError(t, os.WriteFile(controlPath, []byte("0"), 0o600))
	require.NoError(t, os.WriteFile(eventsPath, []byte("populated 1\nfrozen 0\n"), 0o600))
	freezer := &directPodCgroupFreezer{controlPath: controlPath, statePath: eventsPath, version: 2}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)

	mirrorDone := make(chan struct{})
	go func() {
		defer close(mirrorDone)
		mirrorCgroupV2State(ctx, controlPath, eventsPath)
	}()
	defer func() {
		cancel()
		<-mirrorDone
	}()

	requested, err := freezer.Freeze(ctx)
	require.NoError(t, err)
	require.True(t, requested)
	state, err := freezer.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, podCgroupStateFrozen, state)
	require.NoError(t, freezer.Thaw(ctx))
	state, err = freezer.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, podCgroupStateThawed, state)
}

func mirrorCgroupV2State(ctx context.Context, controlPath, eventsPath string) {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			control, err := os.ReadFile(controlPath)
			state := strings.TrimSpace(string(control))
			if err == nil && (state == "0" || state == "1") {
				tempPath := eventsPath + ".tmp"
				if os.WriteFile(tempPath, []byte("populated 1\nfrozen "+state+"\n"), 0o600) == nil {
					_ = os.Rename(tempPath, eventsPath)
				}
			}
		}
	}
}

func TestDirectPodCgroupFreezerHonorsDeadline(t *testing.T) {
	dir := t.TempDir()
	controlPath := filepath.Join(dir, "cgroup.freeze")
	eventsPath := filepath.Join(dir, "cgroup.events")
	require.NoError(t, os.WriteFile(controlPath, []byte("0"), 0o600))
	require.NoError(t, os.WriteFile(eventsPath, []byte("frozen 0\n"), 0o600))
	freezer := &directPodCgroupFreezer{controlPath: controlPath, statePath: eventsPath, version: 2}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	requested, err := freezer.Freeze(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.True(t, requested, "a timed-out freeze request still needs recovery")
}

func TestDirectPodCgroupFreezerRejectsPreFrozenPod(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "freezer.state")
	require.NoError(t, os.WriteFile(statePath, []byte("FROZEN"), 0o600))
	freezer := &directPodCgroupFreezer{controlPath: statePath, statePath: statePath, version: 1}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	requested, err := freezer.Freeze(ctx)
	require.ErrorContains(t, err, "cgroup is frozen")
	assert.False(t, requested)
}

func TestDirectPodCgroupFreezerCanceledBeforeFreeze(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "freezer.state")
	require.NoError(t, os.WriteFile(statePath, []byte("THAWED"), 0o600))
	freezer := &directPodCgroupFreezer{controlPath: statePath, statePath: statePath, version: 1}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	requested, err := freezer.Freeze(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, requested)
	state, err := freezer.State(t.Context())
	require.NoError(t, err)
	assert.Equal(t, podCgroupStateThawed, state)
}
