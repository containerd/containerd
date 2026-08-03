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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestCopyNoFollowRegularFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	require.NoError(t, os.WriteFile(src, []byte("hello"), 0o644))

	require.NoError(t, copyNoFollow(src, dst, 0o600))

	data, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))

	info, err := os.Stat(dst)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestCopyNoFollowMissingSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "does-not-exist")
	dst := filepath.Join(dir, "dst")

	err := copyNoFollow(src, dst, 0o600)
	require.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist), "expected ErrNotExist, got %v", err)
	assert.NoFileExists(t, dst)
}

func TestCopyNoFollowSymlinkSourceNotFollowed(t *testing.T) {
	dir := t.TempDir()

	// A stand-in for a file outside the copy that a symlink might point at.
	secret := filepath.Join(dir, "outside-target")
	require.NoError(t, os.WriteFile(secret, []byte("outside-content"), 0o600))

	src := filepath.Join(dir, "container.log")
	require.NoError(t, os.Symlink(secret, src))
	dst := filepath.Join(dir, "dst")

	err := copyNoFollow(src, dst, 0o600)
	require.Error(t, err)
	// A symlink is not a "missing file"; it must surface as an error, not be skipped.
	assert.False(t, errors.Is(err, os.ErrNotExist))
	// And the linked-to content must not have been copied into the destination.
	assert.NoFileExists(t, dst)
}

func TestCopyNoFollowRejectsFIFO(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "fifo")
	require.NoError(t, unix.Mkfifo(src, 0o600))
	dst := filepath.Join(dir, "dst")

	// Must return promptly with an error rather than blocking on the FIFO open.
	err := copyNoFollow(src, dst, 0o600)
	require.Error(t, err)
	assert.False(t, errors.Is(err, os.ErrNotExist))
	assert.NoFileExists(t, dst)
}

func TestCopyNoFollowRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "adir")
	require.NoError(t, os.Mkdir(src, 0o700))
	dst := filepath.Join(dir, "dst")

	err := copyNoFollow(src, dst, 0o600)
	require.Error(t, err)
	assert.NoFileExists(t, dst)
}

func TestResolveCriuPath(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "criu-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create a dummy executable criu
	execDir := filepath.Join(tempDir, "bin-exec")
	if err := os.MkdirAll(execDir, 0755); err != nil {
		t.Fatal(err)
	}
	execPath := filepath.Join(execDir, "criu")
	if err := os.WriteFile(execPath, []byte("dummy"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create a dummy non-executable criu
	nonExecDir := filepath.Join(tempDir, "bin-nonexec")
	if err := os.MkdirAll(nonExecDir, 0755); err != nil {
		t.Fatal(err)
	}
	nonExecPath := filepath.Join(nonExecDir, "criu")
	if err := os.WriteFile(nonExecPath, []byte("dummy"), 0644); err != nil {
		t.Fatal(err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relDir, err := filepath.Rel(wd, execDir)
	if err != nil {
		t.Fatal(err)
	}

	// Mock the system PATH to point to our executable directory
	t.Setenv("PATH", execDir)

	tests := []struct {
		name         string
		customPath   string
		expectedPath string
	}{
		{
			name:         "custom PATH with executable criu",
			customPath:   execDir,
			expectedPath: execPath,
		},
		{
			name:         "custom PATH with non-executable criu",
			customPath:   nonExecDir,
			expectedPath: "",
		},
		{
			name:         "multiple directories in PATH, executable in second",
			customPath:   nonExecDir + string(filepath.ListSeparator) + execDir,
			expectedPath: execPath,
		},
		{
			name:         "custom PATH with relative directory is skipped",
			customPath:   relDir,
			expectedPath: "",
		},
		{
			name:         "empty customPath falls back to system PATH",
			customPath:   "",
			expectedPath: execPath, // Falls back to system PATH (mocked to execDir)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := resolveCriuPath(tt.customPath)
			if path != tt.expectedPath {
				t.Errorf("expected %s, got %s", tt.expectedPath, path)
			}
		})
	}
}

func TestCheckCriuDisabled(t *testing.T) {
	c := newTestCRIService()
	c.config.EnableCRIU = func() *bool { v := false; return &v }()
	err := c.checkCriu()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "criu support is disabled by configuration") {
		t.Errorf("expected error containing 'criu support is disabled by configuration', got: %v", err)
	}
}

func TestCheckCriuEnabled(t *testing.T) {
	t.Setenv("PATH", "")
	c := newTestCRIService()
	c.config.EnableCRIU = func() *bool { v := true; return &v }()
	err := c.checkCriu()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), "criu support is disabled by configuration") {
		t.Errorf("did not expect error containing 'criu support is disabled by configuration', got: %v", err)
	}
}

func TestCheckpointContainerDisabled(t *testing.T) {
	c := newTestCRIService()
	c.config.EnableCRIU = func() *bool { v := false; return &v }()
	_, err := c.CheckpointContainer(context.Background(), &runtime.CheckpointContainerRequest{
		ContainerId: "test-container",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "criu support is disabled by configuration") {
		t.Errorf("expected error containing 'criu support is disabled by configuration', got: %v", err)
	}
}
