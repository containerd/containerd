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
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	spec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestWriteCheckpointArchiveAtomicReplacesDestination(t *testing.T) {
	checkpointDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(checkpointDir, "payload"), []byte("checkpoint"), 0o600))
	destinationDir := t.TempDir()
	destination := filepath.Join(destinationDir, "checkpoint.tar")
	require.NoError(t, os.WriteFile(destination, make([]byte, 1<<20), 0o666))

	require.NoError(t, writeCheckpointArchiveAtomic(context.Background(), destination, checkpointDir))
	info, err := os.Stat(destination)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	temporaryFiles, err := filepath.Glob(filepath.Join(destinationDir, ".checkpoint-tmp-*"))
	require.NoError(t, err)
	assert.Empty(t, temporaryFiles)

	archiveFile, err := os.Open(destination)
	require.NoError(t, err)
	defer archiveFile.Close()
	reader := tar.NewReader(archiveFile)
	foundPayload := false
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if filepath.Base(header.Name) != "payload" {
			continue
		}
		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		assert.Equal(t, "checkpoint", string(data))
		foundPayload = true
	}
	assert.True(t, foundPayload)
}

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

func TestCopyCheckpointMetadataIfMissing(t *testing.T) {
	t.Run("preserves current checkpoint metadata", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "state", "dump.log")
		dst := filepath.Join(dir, "checkpoint", "dump.log")
		require.NoError(t, os.MkdirAll(filepath.Dir(src), 0o700))
		require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o700))
		require.NoError(t, os.WriteFile(src, []byte("stale"), 0o600))
		require.NoError(t, os.WriteFile(dst, []byte("current"), 0o600))

		require.NoError(t, copyCheckpointMetadataIfMissing(src, dst))
		data, err := os.ReadFile(dst)
		require.NoError(t, err)
		assert.Equal(t, "current", string(data))
	})

	t.Run("copies runtime fallback", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "state", "status")
		dst := filepath.Join(dir, "checkpoint", "status")
		require.NoError(t, os.MkdirAll(filepath.Dir(src), 0o700))
		require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o700))
		require.NoError(t, os.WriteFile(src, []byte("runtime metadata"), 0o644))

		require.NoError(t, copyCheckpointMetadataIfMissing(src, dst))
		data, err := os.ReadFile(dst)
		require.NoError(t, err)
		assert.Equal(t, "runtime metadata", string(data))
		info, err := os.Stat(dst)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})

	t.Run("allows missing optional metadata", func(t *testing.T) {
		dir := t.TempDir()
		err := copyCheckpointMetadataIfMissing(
			filepath.Join(dir, "missing-source"),
			filepath.Join(dir, "missing-destination"),
		)
		assert.NoError(t, err)
	})

	t.Run("rejects non-regular current metadata", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "state")
		dst := filepath.Join(dir, "checkpoint")
		require.NoError(t, os.WriteFile(src, []byte("fallback"), 0o600))
		require.NoError(t, os.Symlink(src, dst))

		err := copyCheckpointMetadataIfMissing(src, dst)
		require.ErrorContains(t, err, "is not a regular file")
	})
}

func TestAssertCheckpointDirSafe(t *testing.T) {
	t.Run("regular files and dirs allowed", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "checkpoint"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(root, "checkpoint", "img"), []byte("x"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(root, "rootfs-diff.tar"), []byte("x"), 0o600))
		assert.NoError(t, assertCheckpointDirSafe(root))
	})

	t.Run("symlink rejected", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.Symlink("/some/outside/path", filepath.Join(root, "rootfs-diff.tar")))
		assert.Error(t, assertCheckpointDirSafe(root))
	})

	t.Run("symlink nested in subdir rejected", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "checkpoint"), 0o700))
		require.NoError(t, os.Symlink("/some/outside/path", filepath.Join(root, "checkpoint", "pages-1.img")))
		assert.Error(t, assertCheckpointDirSafe(root))
	})

	t.Run("fifo rejected", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, unix.Mkfifo(filepath.Join(root, "fifo"), 0o600))
		assert.Error(t, assertCheckpointDirSafe(root))
	})
}

func TestCheckpointArchiveEntryAllowed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typ     byte
		allowed bool
	}{
		{"regular", tar.TypeReg, true},
		//nolint:staticcheck // TypeRegA is deprecated but external tars may still use it
		{"regular-A", tar.TypeRegA, true},
		{"directory", tar.TypeDir, true},
		{"global-header", tar.TypeXGlobalHeader, true},
		{"symlink", tar.TypeSymlink, false},
		{"hardlink", tar.TypeLink, false},
		{"char-device", tar.TypeChar, false},
		{"block-device", tar.TypeBlock, false},
		{"fifo", tar.TypeFifo, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkpointArchiveEntryAllowed(&tar.Header{Typeflag: tc.typ, Name: tc.name})
			assert.Equal(t, tc.allowed, got)
		})
	}
}

type testLogHook struct {
	mu      sync.Mutex
	entries []string
}

func (h *testLogHook) Levels() []logrus.Level {
	return []logrus.Level{logrus.WarnLevel}
}

func (h *testLogHook) Fire(entry *logrus.Entry) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append(h.entries, entry.Message)
	return nil
}

func TestFilterAndMergeAnnotations(t *testing.T) {
	for desc, tc := range map[string]struct {
		checkpointAnnotations map[string]string
		createAnnotations     map[string]string
		expectedAnnotations   map[string]string
		expectedWarnings      []string
	}{
		"cdi denied prefix boundaries": {
			checkpointAnnotations: map[string]string{
				"cdi.k8s.io/device":   "gpu",
				"cdi.k8s.io/":         "true",
				"cdi.k8s.io":          "true",
				"safe.org/cdi.k8s.io": "ignored",
				"other":               "val",
			},
			expectedAnnotations: map[string]string{
				"safe.org/cdi.k8s.io": "ignored",
				"other":               "val",
			},
			expectedWarnings: []string{
				`Denying annotation "cdi.k8s.io/device" in checkpoint restore`,
				`Denying annotation "cdi.k8s.io/" in checkpoint restore`,
				`Denying annotation "cdi.k8s.io" in checkpoint restore`,
			},
		},

		"restore request metadata is authoritative": {
			checkpointAnnotations: map[string]string{
				"io.kubernetes.container.hash":         "old-hash",
				"io.kubernetes.container.restartCount": "1",
				"safe.annotation":                      "old",
				"checkpoint.only":                      "preserved",
			},
			createAnnotations: map[string]string{
				"cdi.k8s.io/device":                    "current-gpu",
				"io.kubernetes.container.hash":         "new-hash",
				"io.kubernetes.container.restartCount": "2",
				"safe.annotation":                      "new",
				"request.only":                         "added",
			},
			expectedAnnotations: map[string]string{
				"cdi.k8s.io/device":                    "current-gpu",
				"io.kubernetes.container.hash":         "new-hash",
				"io.kubernetes.container.restartCount": "2",
				"safe.annotation":                      "new",
				"checkpoint.only":                      "preserved",
				"request.only":                         "added",
			},
		},
	} {
		t.Run(desc, func(t *testing.T) {
			logger := logrus.New()
			logger.SetLevel(logrus.WarnLevel)
			hook := &testLogHook{}
			logger.AddHook(hook)
			ctx := log.WithLogger(context.Background(), logrus.NewEntry(logger))

			res := filterAndMergeAnnotations(
				ctx,
				tc.checkpointAnnotations,
				tc.createAnnotations,
			)

			assert.Equal(t, tc.expectedAnnotations, res)
			assert.ElementsMatch(t, tc.expectedWarnings, hook.entries)
		})
	}
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

func TestMergeStringMaps(t *testing.T) {
	base := map[string]string{"base": "kept", "shared": "old"}
	overrides := map[string]string{"request": "added", "shared": "new"}

	result := mergeStringMaps(base, overrides)
	assert.Equal(t, map[string]string{
		"base":    "kept",
		"request": "added",
		"shared":  "new",
	}, result)

	result["base"] = "changed"
	result["request"] = "changed"
	assert.Equal(t, "kept", base["base"], "result must not alias checkpoint metadata")
	assert.Equal(t, "added", overrides["request"], "result must not alias request metadata")
}

func TestRestoreContainerMetadata(t *testing.T) {
	checkpointLabels := map[string]string{"checkpoint-only": "stale", "shared": "old"}
	checkpointAnnotations := map[string]string{"checkpoint-only": "stale", "shared": "old"}
	createLabels := map[string]string{"request-only": "current", "shared": "new"}
	createAnnotations := map[string]string{"request-only": "current", "shared": "new"}

	labels, annotations := restoreContainerMetadata(
		withPodRestoreContainerContext(context.Background()),
		checkpointLabels,
		checkpointAnnotations,
		createLabels,
		createAnnotations,
		"new-uid",
	)
	assert.Equal(t, createLabels, labels)
	assert.Equal(t, createAnnotations, annotations)
	assert.NotContains(t, labels, "checkpoint-only")
	assert.NotContains(t, annotations, "checkpoint-only")

	labels["request-only"] = "mutated"
	annotations["request-only"] = "mutated"
	assert.Equal(t, "current", createLabels["request-only"])
	assert.Equal(t, "current", createAnnotations["request-only"])
}

func checkpointProcessFixture() (*runtime.ContainerConfig, *v1.ImageConfig, *spec.Spec) {
	config := &runtime.ContainerConfig{
		Image:      &runtime.ImageSpec{Image: "sha256:image", UserSpecifiedImage: "busybox"},
		Args:       []string{"serve"},
		WorkingDir: "/work",
		Envs: []*runtime.KeyValue{
			{Key: "FROM_CONFIG", Value: "current"},
		},
		Linux: &runtime.LinuxContainerConfig{
			SecurityContext: &runtime.LinuxContainerSecurityContext{
				RunAsUser:          &runtime.Int64Value{Value: 1000},
				RunAsGroup:         &runtime.Int64Value{Value: 2000},
				SupplementalGroups: []int64{3000},
				NoNewPrivs:         true,
			},
		},
	}
	image := &v1.ImageConfig{
		Entrypoint: []string{"/bin/app"},
		Cmd:        []string{"default"},
		Env:        []string{"FROM_IMAGE=base"},
		WorkingDir: "/image-work",
	}
	dump := &spec.Spec{Process: &spec.Process{
		Args:            []string{"/bin/app", "serve"},
		Cwd:             "/work",
		Env:             []string{"PATH=/usr/bin", "HOSTNAME=pod", "FROM_IMAGE=base", "FROM_CONFIG=current"},
		NoNewPrivileges: true,
		User: spec.User{
			UID:            1000,
			GID:            2000,
			AdditionalGids: []uint32{3000},
		},
	}}
	return config, image, dump
}

func TestValidateRestoreContainerConfig(t *testing.T) {
	checkpoint, _, _ := checkpointProcessFixture()
	restore, _, _ := checkpointProcessFixture()
	restore.Image.UserSpecifiedImage = "docker.io/library/busybox:latest"
	require.NoError(t, validateRestoreContainerConfig(checkpoint, restore))

	tests := []struct {
		name    string
		mutate  func(*runtime.ContainerConfig)
		wantErr string
	}{
		{name: "image", mutate: func(c *runtime.ContainerConfig) { c.Image.UserSpecifiedImage = "alpine" }, wantErr: "restore image"},
		{name: "command", mutate: func(c *runtime.ContainerConfig) { c.Command = []string{"/bin/other"} }, wantErr: "restore command"},
		{name: "arguments", mutate: func(c *runtime.ContainerConfig) { c.Args = []string{"other"} }, wantErr: "restore arguments"},
		{name: "working directory", mutate: func(c *runtime.ContainerConfig) { c.WorkingDir = "/other" }, wantErr: "working directory"},
		{name: "environment", mutate: func(c *runtime.ContainerConfig) { c.Envs[0].Value = "other" }, wantErr: "restore environment"},
		{name: "security", mutate: func(c *runtime.ContainerConfig) { c.Linux.SecurityContext.RunAsUser.Value = 42 }, wantErr: "security context"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			restore, _, _ := checkpointProcessFixture()
			test.mutate(restore)
			err := validateRestoreContainerConfig(checkpoint, restore)
			require.ErrorContains(t, err, test.wantErr)
			require.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
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

func TestCRImportCheckpointDisabled(t *testing.T) {
	c := newTestCRIService()
	c.config.EnableCRIU = func() *bool { v := false; return &v }()
	_, err := c.CRImportCheckpoint(context.Background(), nil, nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "criu support is disabled by configuration") {
		t.Errorf("expected error containing 'criu support is disabled by configuration', got: %v", err)
	}
}

func TestValidateCheckpointProcess(t *testing.T) {
	config, image, dump := checkpointProcessFixture()
	require.NoError(t, validateCheckpointProcess(dump, config, image))

	tests := []struct {
		name    string
		mutate  func(*spec.Spec)
		wantErr string
	}{
		{name: "arguments", mutate: func(s *spec.Spec) { s.Process.Args[1] = "other" }, wantErr: "process arguments"},
		{name: "working directory", mutate: func(s *spec.Spec) { s.Process.Cwd = "/other" }, wantErr: "working directory"},
		{name: "environment", mutate: func(s *spec.Spec) { s.Process.Env[3] = "FROM_CONFIG=other" }, wantErr: "environment variable"},
		{name: "uid", mutate: func(s *spec.Spec) { s.Process.User.UID = 42 }, wantErr: "process UID"},
		{name: "gid", mutate: func(s *spec.Spec) { s.Process.User.GID = 42 }, wantErr: "process GID"},
		{name: "supplemental group", mutate: func(s *spec.Spec) { s.Process.User.AdditionalGids = nil }, wantErr: "supplemental group"},
		{name: "no new privileges", mutate: func(s *spec.Spec) { s.Process.NoNewPrivileges = false }, wantErr: "no-new-privileges"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, checkpoint := checkpointProcessFixture()
			test.mutate(checkpoint)
			err := validateCheckpointProcess(checkpoint, config, image)
			require.ErrorContains(t, err, test.wantErr)
			require.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
		})
	}
}

func TestValidateCheckpointRuntime(t *testing.T) {
	tests := []struct {
		name              string
		checkpointRuntime string
		restoreRuntime    string
		wantErr           string
	}{
		{name: "matching", checkpointRuntime: "io.containerd.runc.v2", restoreRuntime: "io.containerd.runc.v2"},
		{name: "legacy checkpoint", restoreRuntime: "io.containerd.runc.v2"},
		{
			name:              "mismatch",
			checkpointRuntime: "io.containerd.runc.v2",
			restoreRuntime:    "io.containerd.runsc.v1",
			wantErr:           `checkpoint requires runtime "io.containerd.runc.v2", but restored sandbox uses runtime "io.containerd.runsc.v1": failed precondition`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateCheckpointRuntime(test.checkpointRuntime, test.restoreRuntime)
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, test.wantErr)
			require.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
		})
	}
}
