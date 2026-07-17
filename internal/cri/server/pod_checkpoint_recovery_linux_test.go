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
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPodCheckpointMarkerRoundTrip(t *testing.T) {
	c := newTestCRIService()
	c.config.RootDir = t.TempDir()
	marker := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
		SandboxID:    "sandbox-1",
		CgroupParent: "/kubepods/pod-1",
		ContainerIDs: []string{"container-a", "container-b"},
	}

	active, err := c.writePodCheckpointMarker(marker)
	require.NoError(t, err)
	require.True(t, active)
	markerInfo, err := os.Lstat(c.podCheckpointMarkerPath(marker.SandboxID))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), markerInfo.Mode().Perm())
	dirInfo, err := os.Lstat(c.podCheckpointMarkerDirectory())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())

	markers, err := c.loadPodCheckpointMarkers()
	require.NoError(t, err)
	require.Equal(t, []podCheckpointRecoveryMarker{marker}, markers)
	active, err = c.writePodCheckpointMarker(marker)
	require.ErrorContains(t, err, "already exists")
	require.False(t, active)

	require.NoError(t, c.removePodCheckpointMarker(marker.SandboxID))
	markers, err = c.loadPodCheckpointMarkers()
	require.NoError(t, err)
	assert.Empty(t, markers)
}

func TestPodCheckpointMarkerPostPublishFailureRollsBack(t *testing.T) {
	c := newTestCRIService()
	c.config.RootDir = t.TempDir()
	marker := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
		SandboxID:    "sandbox-1",
		CgroupParent: "/kubepods/pod-1",
		ContainerIDs: []string{"container-a"},
	}
	syncCalls := 0
	operations := defaultPodCheckpointMarkerFileOperations
	operations.syncDirectory = func(path string) error {
		syncCalls++
		if syncCalls == 1 {
			return errors.New("injected publish sync failure")
		}
		return syncDirectory(path)
	}

	active, err := c.writePodCheckpointMarkerWithFileOperations(marker, operations)
	require.ErrorContains(t, err, "injected publish sync failure")
	assert.False(t, active)
	_, statErr := os.Lstat(c.podCheckpointMarkerPath(marker.SandboxID))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestPodCheckpointMarkerPostPublishRollbackFailureStaysActive(t *testing.T) {
	c := newTestCRIService()
	c.config.RootDir = t.TempDir()
	marker := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
		SandboxID:    "sandbox-1",
		CgroupParent: "/kubepods/pod-1",
		ContainerIDs: []string{"container-a"},
	}
	operations := defaultPodCheckpointMarkerFileOperations
	operations.syncDirectory = func(string) error { return errors.New("injected publish sync failure") }
	operations.remove = func(path string) error {
		if path == c.podCheckpointMarkerPath(marker.SandboxID) {
			return errors.New("injected rollback failure")
		}
		return os.Remove(path)
	}

	active, err := c.writePodCheckpointMarkerWithFileOperations(marker, operations)
	require.ErrorContains(t, err, "injected publish sync failure")
	require.ErrorContains(t, err, "injected rollback failure")
	assert.True(t, active)
	_, statErr := os.Lstat(c.podCheckpointMarkerPath(marker.SandboxID))
	require.NoError(t, statErr)
}

func TestValidatePodCheckpointMarker(t *testing.T) {
	valid := podCheckpointRecoveryMarker{
		Version:      podCheckpointMarkerVersion,
		SandboxID:    "sandbox-1",
		CgroupParent: "/kubepods/pod-1",
		ContainerIDs: []string{"container-a"},
	}
	for _, test := range []struct {
		name    string
		mutate  func(*podCheckpointRecoveryMarker)
		wantErr string
	}{
		{name: "valid"},
		{name: "version", mutate: func(marker *podCheckpointRecoveryMarker) { marker.Version++ }, wantErr: "version"},
		{name: "sandbox", mutate: func(marker *podCheckpointRecoveryMarker) { marker.SandboxID = "" }, wantErr: "no sandbox ID"},
		{name: "cgroup", mutate: func(marker *podCheckpointRecoveryMarker) { marker.CgroupParent = "/" }, wantErr: "invalid cgroup parent"},
		{name: "containers", mutate: func(marker *podCheckpointRecoveryMarker) { marker.ContainerIDs = nil }, wantErr: "no container IDs"},
		{name: "duplicate", mutate: func(marker *podCheckpointRecoveryMarker) {
			marker.ContainerIDs = []string{"container-a", "container-a"}
		}, wantErr: "duplicate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			marker := valid
			marker.ContainerIDs = append([]string(nil), valid.ContainerIDs...)
			if test.mutate != nil {
				test.mutate(&marker)
			}
			err := validatePodCheckpointMarker(marker)
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}
