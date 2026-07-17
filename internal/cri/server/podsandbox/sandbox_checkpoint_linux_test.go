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
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCheckpointServiceOwnsItsState(t *testing.T) {
	_, err := NewCheckpointService(CheckpointServiceOptions{RootDir: t.TempDir()})
	require.ErrorContains(t, err, "containerd client")

	_, err = NewCheckpointService(CheckpointServiceOptions{Client: new(client.Client)})
	require.ErrorContains(t, err, "root directory")

	root := filepath.Join(t.TempDir(), "controller-checkpoints")
	service, err := NewCheckpointService(CheckpointServiceOptions{
		Client:  new(client.Client),
		RootDir: root,
	})
	require.NoError(t, err)
	assert.Equal(t, root, service.rootDir)
	info, err := os.Stat(root)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	symlink := filepath.Join(t.TempDir(), "controller-checkpoints")
	require.NoError(t, os.Symlink(root, symlink))
	_, err = NewCheckpointService(CheckpointServiceOptions{
		Client:  new(client.Client),
		RootDir: symlink,
	})
	require.ErrorContains(t, err, "not a real directory")
}

func TestCheckpointServiceRequiresCriu(t *testing.T) {
	// A custom shim PATH must take precedence over the daemon's PATH.
	systemPath := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(systemPath, "criu"), []byte("#!/bin/sh\nexit 1\n"), 0o700))
	t.Setenv("PATH", systemPath)
	service, err := NewCheckpointService(CheckpointServiceOptions{
		Client:   new(client.Client),
		RootDir:  t.TempDir(),
		ShimPath: t.TempDir(),
	})
	require.NoError(t, err, "CRIU is checked when a checkpoint or restore is requested")

	err = service.Checkpoint(namespaces.WithNamespace(t.Context(), "k8s.io"), "sandbox", sandbox.CheckpointOptions{})
	require.ErrorContains(t, err, "criu binary not found")
}

func TestCheckpointOutputReservation(t *testing.T) {
	service := new(CheckpointService)
	output := t.TempDir()

	_, release, err := service.reservePodCheckpointOutput(output)
	require.NoError(t, err)
	existing := filepath.Join(output, "existing")
	require.NoError(t, os.WriteFile(existing, []byte("data"), 0o600))

	// Check reservation ownership before inspecting contents, which the active
	// checkpoint can populate after another request resolves the same path.
	_, _, err = service.reservePodCheckpointOutput(output)
	require.ErrorContains(t, err, "already in use")
	release()

	_, _, err = service.reservePodCheckpointOutput(output)
	require.ErrorContains(t, err, "must be empty")
	require.NoError(t, os.Remove(existing))

	// A failed emptiness check must release the reservation for a later request.
	_, release, err = service.reservePodCheckpointOutput(output)
	require.NoError(t, err)
	release()
}

func TestValidateCheckpointOutputPathRejectsUnsafePaths(t *testing.T) {
	require.Error(t, validateCheckpointOutputPath("relative"))

	parent := t.TempDir()
	realDir := filepath.Join(parent, "real")
	require.NoError(t, os.Mkdir(realDir, 0o700))
	symlink := filepath.Join(parent, "link")
	require.NoError(t, os.Symlink(realDir, symlink))
	require.ErrorContains(t, validateCheckpointOutputPath(symlink), "not a real directory")

	file := filepath.Join(parent, "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	require.ErrorContains(t, validateCheckpointOutputPath(file), "not a real directory")
}

func TestCheckpointOutputReservationReleasesRejectedSymlink(t *testing.T) {
	service := new(CheckpointService)
	output := t.TempDir()
	symlink := filepath.Join(t.TempDir(), "output")
	require.NoError(t, os.Symlink(output, symlink))

	_, _, err := service.reservePodCheckpointOutput(symlink)
	require.ErrorContains(t, err, "not a real directory")

	_, release, err := service.reservePodCheckpointOutput(output)
	require.NoError(t, err)
	release()
}

func TestPodCheckpointOptionsAreControllerSpecific(t *testing.T) {
	require.NoError(t, validatePodCheckpointOptions(nil))

	err := validatePodCheckpointOptions(map[string]string{"future-controller-option": "value"})
	require.ErrorIs(t, err, errdefs.ErrInvalidArgument)
	require.ErrorContains(t, err, "pause controller")
}

func TestCheckpointNamesAreSafeAndScoped(t *testing.T) {
	archive := checkpointArchiveName("../../container")
	assert.Equal(t, filepath.Base(archive), archive)
	assert.NotContains(t, archive, "..")
}

func TestWritePodCheckpointMetadata(t *testing.T) {
	for _, name := range []string{podCheckpointConfigFile, podCheckpointManifestFile} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			data := []byte(`{"version":2}`)
			require.NoError(t, writePodCheckpointMetadata(path, data))
			actual, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, data, actual)
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Len(t, entries, 1, "publication must not leave temporary metadata files")
		})
	}
	t.Run("publication failure", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, podCheckpointManifestFile)
		require.NoError(t, os.Mkdir(path, 0o700))
		require.Error(t, writePodCheckpointMetadata(path, []byte(`{}`)))
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 1, "failed publication must remove its temporary file")
		require.True(t, entries[0].IsDir())
	})
}
