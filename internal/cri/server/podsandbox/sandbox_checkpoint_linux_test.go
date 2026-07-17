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
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

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

	ctx := namespaces.WithNamespace(t.Context(), "k8s.io")
	// Checkpoint checks CRIU after it validates the selected containers.
	err = service.checkCriu()
	require.ErrorContains(t, err, "criu binary not found")
	require.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
	_, err = service.Restore(ctx, "sandbox", sandbox.RestoreOptions{})
	require.ErrorContains(t, err, "criu binary not found")
	require.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
}

func TestCheckPodCheckpointRuntime(t *testing.T) {
	require.NoError(t, checkPodCheckpointRuntime("container", "io.containerd.runc.v2"))
	for _, runtimeName := range []string{"io.containerd.runsc.v1", "io.containerd.kata.v2", ""} {
		err := checkPodCheckpointRuntime("container", runtimeName)
		require.ErrorIs(t, err, errdefs.ErrNotImplemented, runtimeName)
	}
}

func TestCheckpointSpecFailureCleansWorkDirectory(t *testing.T) {
	root := t.TempDir()
	service := &CheckpointService{rootDir: root}
	container := checkpointContainer{container: &fakeContainer{}}

	err := service.createContainerCheckpointImage(t.Context(), container, "checkpoint")
	require.ErrorIs(t, err, errdefs.ErrNotImplemented)
	require.ErrorContains(t, err, "failed to read checkpoint container spec")
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, entries, "spec failures must remove the private work directory")
}

func TestResolvePodCheckpointOutput(t *testing.T) {
	output := t.TempDir()
	resolved, err := resolvePodCheckpointOutput(output)
	require.NoError(t, err)
	canonical, err := filepath.EvalSymlinks(output)
	require.NoError(t, err)
	assert.Equal(t, canonical, resolved)

	require.NoError(t, os.WriteFile(filepath.Join(output, "existing"), []byte("data"), 0o600))
	_, err = resolvePodCheckpointOutput(output)
	require.ErrorContains(t, err, "must be empty")

	symlink := filepath.Join(t.TempDir(), "output")
	require.NoError(t, os.Symlink(t.TempDir(), symlink))
	_, err = resolvePodCheckpointOutput(symlink)
	require.ErrorContains(t, err, "not a real directory")
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

func TestPodCheckpointOptionsAreControllerSpecific(t *testing.T) {
	for _, options := range []map[string]string{nil, {}} {
		require.NoError(t, validatePodCheckpointOptions(options))
		require.NoError(t, validatePodRestoreOptions(options))
	}

	for _, operation := range []string{"checkpoint", "restore"} {
		t.Run(operation, func(t *testing.T) {
			root := t.TempDir()
			path := t.TempDir()
			// A nil client makes any attempt to use containerd services fail.
			service := &CheckpointService{rootDir: root}
			// Restore checks CRIU before options. Cache success without running CRIU.
			service.checkCriuOnce.Do(func() {})
			controller := &Controller{checkpoint: service}
			enabled := true
			controller.config.EnableCheckpointRestore = &enabled
			ctx, cancel := context.WithTimeout(namespaces.WithNamespace(t.Context(), "k8s.io"), time.Minute)
			defer cancel()
			options := map[string]string{"future-controller-option": "value"}

			var err error
			if operation == "checkpoint" {
				err = controller.Checkpoint(ctx, "sandbox", sandbox.CheckpointOptions{OutputPath: path, Options: options})
			} else {
				result, restoreErr := controller.Restore(ctx, "sandbox", sandbox.RestoreOptions{CheckpointPath: path, Options: options})
				require.Empty(t, result)
				err = restoreErr
			}
			require.ErrorIs(t, err, errdefs.ErrInvalidArgument)
			require.ErrorContains(t, err, "pause controller")
			for _, directory := range []string{root, path} {
				entries, err := os.ReadDir(directory)
				require.NoError(t, err)
				require.Empty(t, entries)
			}
		})
	}
}

func TestCheckpointNamesAreSafeAndScoped(t *testing.T) {
	archive := checkpointArchiveName("../../container")
	assert.Equal(t, filepath.Base(archive), archive)
	assert.NotContains(t, archive, "..")
	assert.NotEqual(t,
		restoreCheckpointImageName("sandbox-a", "container"),
		restoreCheckpointImageName("sandbox-b", "container"),
	)
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
	t.Run("write failure", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, podCheckpointManifestFile)
		original := []byte(`{"version":2}`)
		require.NoError(t, os.WriteFile(path, original, 0o600))
		writeErr := errors.New("injected write failure")
		err := writeCheckpointFileAtomic(path, checkpointMetadataTempPrefix, func(w io.Writer) error {
			_, err := w.Write([]byte(`{"version":`))
			require.NoError(t, err)
			return writeErr
		})
		require.ErrorIs(t, err, writeErr)
		actual, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, original, actual)
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 1, "failed writes must remove their temporary file")
	})
}

func TestControllerChecksCheckpointRestoreSetting(t *testing.T) {
	disabled := false
	c := &Controller{checkpoint: new(CheckpointService)}
	c.config.EnableCheckpointRestore = &disabled

	err := c.Checkpoint(t.Context(), "sandbox", sandbox.CheckpointOptions{})
	require.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
	require.ErrorContains(t, err, "disabled by configuration")
	_, err = c.Restore(t.Context(), "sandbox", sandbox.RestoreOptions{})
	require.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
}
