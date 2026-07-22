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

package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/client"
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

func TestDirectPodCgroupFreezerV1(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "freezer.state")
	require.NoError(t, os.WriteFile(statePath, []byte("THAWED"), 0o600))
	freezer := &directPodCgroupFreezer{controlPath: statePath, statePath: statePath, version: 1}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.NoError(t, freezer.Freeze(ctx))
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
	defer cancel()

	go mirrorCgroupV2State(ctx, controlPath, eventsPath)
	require.NoError(t, freezer.Freeze(ctx))
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

	err := freezer.Freeze(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestDirectPodCgroupFreezerRejectsPreFrozenPod(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "freezer.state")
	require.NoError(t, os.WriteFile(statePath, []byte("FROZEN"), 0o600))
	freezer := &directPodCgroupFreezer{controlPath: statePath, statePath: statePath, version: 1}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.ErrorContains(t, freezer.Freeze(ctx), "cgroup is frozen")
}

func TestPauseAndRecoverPodCheckpointTasks(t *testing.T) {
	var events []string
	freezer := &recordingPodCgroupFreezer{state: podCgroupStateThawed, events: &events}
	tasks := []podCheckpointRuntimeTask{
		&recordingPodCheckpointTask{id: "container-a", status: client.Running, events: &events},
		&recordingPodCheckpointTask{id: "container-b", status: client.Running, events: &events},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.NoError(t, pausePodCheckpointTasks(ctx, freezer, tasks))
	assert.Equal(t, []string{"freeze", "pause:container-a", "pause:container-b", "thaw"}, events)
	require.NoError(t, recoverPodCheckpointTasks(ctx, freezer, tasks))
	assert.Equal(t, []string{
		"freeze", "pause:container-a", "pause:container-b", "thaw",
		"thaw", "resume:container-a", "resume:container-b",
	}, events)
}

func TestPartialPauseCanBeRecovered(t *testing.T) {
	errPause := errors.New("pause failed")
	var events []string
	freezer := &recordingPodCgroupFreezer{state: podCgroupStateThawed, events: &events}
	tasks := []podCheckpointRuntimeTask{
		&recordingPodCheckpointTask{id: "container-a", status: client.Running, events: &events},
		&recordingPodCheckpointTask{id: "container-b", status: client.Running, pauseErr: errPause, events: &events},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := pausePodCheckpointTasks(ctx, freezer, tasks)
	require.ErrorIs(t, err, errPause)
	require.NoError(t, recoverPodCheckpointTasks(ctx, freezer, tasks))
	assert.Equal(t, []string{
		"freeze", "pause:container-a", "pause:container-b",
		"thaw", "resume:container-a",
	}, events)
}

type recordingPodCgroupFreezer struct {
	mu     sync.Mutex
	state  podCgroupState
	events *[]string
}

func (f *recordingPodCgroupFreezer) State(context.Context) (podCgroupState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}

func (f *recordingPodCgroupFreezer) Freeze(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	*f.events = append(*f.events, "freeze")
	f.state = podCgroupStateFrozen
	return nil
}

func (f *recordingPodCgroupFreezer) Thaw(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	*f.events = append(*f.events, "thaw")
	f.state = podCgroupStateThawed
	return nil
}

type recordingPodCheckpointTask struct {
	mu        sync.Mutex
	id        string
	status    client.ProcessStatus
	pauseErr  error
	resumeErr error
	events    *[]string
}

func (t *recordingPodCheckpointTask) ID() string { return t.id }

func (t *recordingPodCheckpointTask) Pause(context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	*t.events = append(*t.events, "pause:"+t.id)
	if t.pauseErr != nil {
		return t.pauseErr
	}
	t.status = client.Paused
	return nil
}

func (t *recordingPodCheckpointTask) Resume(context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	*t.events = append(*t.events, "resume:"+t.id)
	if t.resumeErr != nil {
		return t.resumeErr
	}
	t.status = client.Running
	return nil
}

func (t *recordingPodCheckpointTask) Status(context.Context) (client.Status, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return client.Status{Status: t.status}, nil
}
