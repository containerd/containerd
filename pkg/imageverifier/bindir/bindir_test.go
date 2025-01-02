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

package bindir

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/containerd/containerd/v2/internal/tomlext"
	"github.com/containerd/log"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildGoVerifiers uses the local Go toolchain to build each of the standalone
// main package source files in srcDir into binaries placed in binDir.
func buildGoVerifiers(t *testing.T, srcsDir string, binDir string) {
	srcs, err := os.ReadDir(srcsDir)
	require.NoError(t, err)

	for _, srcFile := range srcs {
		// Build the source into a Go binary.
		src := filepath.Join(srcsDir, srcFile.Name())
		bin := filepath.Join(binDir, strings.Split(srcFile.Name(), ".")[0]+exeIfWindows())
		cmd := exec.Command("go", "build", "-o", bin, src)

		code, err := os.ReadFile(src)
		require.NoError(t, err)

		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("failed to build test verifier %s: %v\n%s\nGo code:\n%s", src, err, out, code)
		}
	}
}

func exeIfWindows() string {
	// The command `go build -o abc abc.go` creates abc.exe on Windows.
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// newBinDir creates a temporary directory and copies each of the selected bins
// fromSrcDir into that directory. The copied verifier binaries are given names
// such that they sort (and therefore execute) in the order that bins is given.
func newBinDir(t *testing.T, srcDir string, bins ...string) string {
	binDir := t.TempDir()

	for i, bin := range bins {
		src, err := os.Open(filepath.Join(srcDir, bin+exeIfWindows()))
		require.NoError(t, err)
		defer src.Close()

		dst, err := os.OpenFile(filepath.Join(binDir, fmt.Sprintf("verifier-%v%v", i, exeIfWindows())), os.O_WRONLY|os.O_CREATE, 0755)
		require.NoError(t, err)
		defer dst.Close()

		_, err = io.Copy(dst, src)
		require.NoError(t, err)
	}

	return binDir
}

func TestBinDirVerifyImage(t *testing.T) {
	// Enable debug logs to easily see stderr for verifiers upon test failure.
	logger := log.L.Dup()
	logger.Logger.SetLevel(log.DebugLevel)
	ctx := log.WithLogger(context.Background(), logger)

	// Build verifiers from plain Go file.
	allBinsDir := t.TempDir()
	buildGoVerifiers(t, "testdata/verifiers", allBinsDir)

	// Build verifiers from templates.
	data := struct {
		ArgsFile  string
		StdinFile string
	}{
		ArgsFile:  filepath.Join(t.TempDir(), "args.txt"),
		StdinFile: filepath.Join(t.TempDir(), "stdin.txt"),
	}

	tmplDir := "testdata/verifier_templates"
	templates, err := os.ReadDir(tmplDir)
	require.NoError(t, err)

	renderedVerifierTmplDir := t.TempDir()
	for _, tmplFile := range templates {
		tmplPath := filepath.Join(tmplDir, tmplFile.Name())

		tmpl, err := template.New(tmplFile.Name()).ParseFiles(tmplPath)
		require.NoError(t, err)

		goFileName := strings.ReplaceAll(tmplFile.Name(), ".go.tmpl", ".go")
		f, err := os.Create(filepath.Join(renderedVerifierTmplDir, goFileName))
		require.NoError(t, err)
		defer f.Close()

		require.NoError(t, tmpl.Execute(f, data))
		f.Close()
	}
	buildGoVerifiers(t, renderedVerifierTmplDir, allBinsDir)

	// Actual tests begin here.
	t.Run("proper input/output management", func(t *testing.T) {
		binDir := newBinDir(t, allBinsDir,
			"verifier_test_input_output_management",
		)

		v := NewImageVerifier(&Config{
			BinDir:             binDir,
			MaxVerifiers:       -1,
			PerVerifierTimeout: tomlext.FromStdTime(5 * time.Second),
		})

		j, err := v.VerifyImage(ctx, "registry.example.com/image:abc", ocispec.Descriptor{
			Digest:      "sha256:98ea6e4f216f2fb4b69fff9b3a44842c38686ca685f3f55dc48c5d3fb1107be4",
			MediaType:   "application/vnd.docker.distribution.manifest.list.v2+json",
			Size:        2048,
			Annotations: map[string]string{"a": "b"},
		})
		assert.NoError(t, err)
		assert.True(t, j.OK)
		assert.Equal(t, fmt.Sprintf("verifier-0%[1]v => Reason A line 1\nReason A line 2", exeIfWindows()), j.Reason)

		b, err := os.ReadFile(data.ArgsFile)
		require.NoError(t, err)
		assert.Equal(t, "-name registry.example.com/image:abc -digest sha256:98ea6e4f216f2fb4b69fff9b3a44842c38686ca685f3f55dc48c5d3fb1107be4 -stdin-media-type application/vnd.oci.descriptor.v1+json", string(b))

		b, err = os.ReadFile(data.StdinFile)
		require.NoError(t, err)
		assert.Equal(t, `{"mediaType":"application/vnd.docker.distribution.manifest.list.v2+json","digest":"sha256:98ea6e4f216f2fb4b69fff9b3a44842c38686ca685f3f55dc48c5d3fb1107be4","size":2048,"annotations":{"a":"b"}}`, strings.TrimSpace(string(b)))
	})

	t.Run("large output is truncated", func(t *testing.T) {
		bins := []string{
			"large_stdout",
			"large_stdout_chunked",
			"large_stderr",
			"large_stderr_chunked",
		}
		binDir := newBinDir(t, allBinsDir, bins...)

		v := NewImageVerifier(&Config{
			BinDir:             binDir,
			MaxVerifiers:       -1,
			PerVerifierTimeout: tomlext.FromStdTime(30 * time.Second),
		})

		j, err := v.VerifyImage(ctx, "registry.example.com/image:abc", ocispec.Descriptor{})
		require.NoError(t, err)
		assert.True(t, j.OK, "expected OK, got not OK with reason: %v", j.Reason)
		assert.Less(t, len(j.Reason), len(bins)*(outputLimitBytes+1024), "reason is: %v", j.Reason) // 1024 leaves margin for the formatting around the reason.
	})

	t.Run("missing directory", func(t *testing.T) {
		v := NewImageVerifier(&Config{
			BinDir:             filepath.Join(t.TempDir(), "missing_directory"),
			MaxVerifiers:       10,
			PerVerifierTimeout: tomlext.FromStdTime(5 * time.Second),
		})

		j, err := v.VerifyImage(ctx, "registry.example.com/image:abc", ocispec.Descriptor{})
		assert.NoError(t, err)
		assert.True(t, j.OK)
		assert.NotEmpty(t, j.Reason)
	})

	t.Run("empty directory", func(t *testing.T) {
		v := NewImageVerifier(&Config{
			BinDir:             t.TempDir(),
			MaxVerifiers:       10,
			PerVerifierTimeout: tomlext.FromStdTime(5 * time.Second),
		})

		j, err := v.VerifyImage(ctx, "registry.example.com/image:abc", ocispec.Descriptor{})
		assert.NoError(t, err)
		assert.True(t, j.OK)
		assert.NotEmpty(t, j.Reason)
	})

	t.Run("max verifiers = 0", func(t *testing.T) {
		binDir := newBinDir(t, allBinsDir,
			"reject_reason_d", // This never runs.
		)

		v := NewImageVerifier(&Config{
			BinDir:             binDir,
			MaxVerifiers:       0,
			PerVerifierTimeout: tomlext.FromStdTime(5 * time.Second),
		})

		j, err := v.VerifyImage(ctx, "registry.example.com/image:abc", ocispec.Descriptor{})
		assert.NoError(t, err)
		assert.True(t, j.OK)
		assert.Empty(t, j.Reason)
	})

	t.Run("max verifiers = 1", func(t *testing.T) {
		binDir := newBinDir(t, allBinsDir,
			"accept_reason_a",
			"reject_reason_d", // This never runs.
		)

		v := NewImageVerifier(&Config{
			BinDir:             binDir,
			MaxVerifiers:       1,
			PerVerifierTimeout: tomlext.FromStdTime(5 * time.Second),
		})

		j, err := v.VerifyImage(ctx, "registry.example.com/image:abc", ocispec.Descriptor{})
		assert.NoError(t, err)
		assert.True(t, j.OK)
		assert.NotEmpty(t, j.Reason)
	})

	t.Run("max verifiers = 2", func(t *testing.T) {
		binDir := newBinDir(t, allBinsDir,
			"accept_reason_a",
			"accept_reason_a",
			"reject_reason_d", // This never runs.
		)

		v := NewImageVerifier(&Config{
			BinDir:             binDir,
			MaxVerifiers:       2,
			PerVerifierTimeout: tomlext.FromStdTime(5 * time.Second),
		})

		j, err := v.VerifyImage(ctx, "registry.example.com/image:abc", ocispec.Descriptor{})
		assert.NoError(t, err)
		assert.True(t, j.OK)
		assert.NotEmpty(t, j.Reason)
	})

	t.Run("max verifiers = 3, all accept", func(t *testing.T) {
		binDir := newBinDir(t, allBinsDir,
			"accept_reason_a",
			"accept_reason_b",
			"accept_reason_c",
		)
		v := NewImageVerifier(&Config{
			BinDir:             binDir,
			MaxVerifiers:       3,
			PerVerifierTimeout: tomlext.FromStdTime(5 * time.Second),
		})

		j, err := v.VerifyImage(ctx, "registry.example.com/image:abc", ocispec.Descriptor{})
		assert.NoError(t, err)
		assert.True(t, j.OK)
		assert.Equal(t, fmt.Sprintf("verifier-0%[1]v => Reason A, verifier-1%[1]v => Reason B, verifier-2%[1]v => Reason C", exeIfWindows()), j.Reason)
	})

	t.Run("max verifiers = 3, with reject", func(t *testing.T) {
		binDir := newBinDir(t, allBinsDir,
			"accept_reason_a",
			"accept_reason_b",
			"reject_reason_d",
		)

		v := NewImageVerifier(&Config{
			BinDir:             binDir,
			MaxVerifiers:       3,
			PerVerifierTimeout: tomlext.FromStdTime(5 * time.Second),
		})

		j, err := v.VerifyImage(ctx, "registry.example.com/image:abc", ocispec.Descriptor{})
		assert.NoError(t, err)
		assert.False(t, j.OK)
		assert.Equal(t, fmt.Sprintf("verifier verifier-2%[1]v rejected image (exit code 1): Reason D", exeIfWindows()), j.Reason)
	})

	t.Run("max verifiers = -1, all accept", func(t *testing.T) {
		binDir := newBinDir(t, allBinsDir,
			"accept_reason_a",
			"accept_reason_b",
			"accept_reason_c",
		)

		v := NewImageVerifier(&Config{
			BinDir:             binDir,
			MaxVerifiers:       -1,
			PerVerifierTimeout: tomlext.FromStdTime(5 * time.Second),
		})

		j, err := v.VerifyImage(ctx, "registry.example.com/image:abc", ocispec.Descriptor{})
		assert.NoError(t, err)
		assert.True(t, j.OK)
		assert.Equal(t, fmt.Sprintf("verifier-0%[1]v => Reason A, verifier-1%[1]v => Reason B, verifier-2%[1]v => Reason C", exeIfWindows()), j.Reason)
	})

	t.Run("max verifiers = -1, with reject", func(t *testing.T) {
		binDir := newBinDir(t, allBinsDir,
			"accept_reason_a",
			"accept_reason_b",
			"reject_reason_d",
		)

		v := NewImageVerifier(&Config{
			BinDir:             binDir,
			MaxVerifiers:       -1,
			PerVerifierTimeout: tomlext.FromStdTime(5 * time.Second),
		})

		j, err := v.VerifyImage(ctx, "registry.example.com/image:abc", ocispec.Descriptor{})
		assert.NoError(t, err)
		assert.False(t, j.OK)
		assert.Equal(t, fmt.Sprintf("verifier verifier-2%[1]v rejected image (exit code 1): Reason D", exeIfWindows()), j.Reason)
	})

	t.Run("max verifiers = -1, with timeout", func(t *testing.T) {
		binDir := newBinDir(t, allBinsDir,
			"accept_reason_a",
			"accept_reason_b",
			"slow_child_process",
		)

		v := NewImageVerifier(&Config{
			BinDir:             binDir,
			MaxVerifiers:       -1,
			PerVerifierTimeout: tomlext.FromStdTime(5 * time.Second),
		})

		j, err := v.VerifyImage(ctx, "registry.example.com/image:abc", ocispec.Descriptor{})
		if runtime.GOOS == "windows" {
			assert.NoError(t, err)
			assert.False(t, j.OK)
			assert.Equal(t, "verifier verifier-2.exe rejected image (exit code 1): ", j.Reason)
		} else {
			assert.ErrorContains(t, err, "signal: killed")
			assert.Nil(t, j)
		}

		command := []string{"ps", "ax"}
		if runtime.GOOS == "windows" {
			command = []string{"tasklist"}
		}
		b, err := exec.Command(command[0], command[1:]...).CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}

		if strings.Contains(string(b), "verifier-") {
			t.Fatalf("killing the verifier binary didn't kill all its children:\n%v", string(b))
		}
	})

	t.Run("max verifiers = -1, with exec failure", func(t *testing.T) {
		binDir := t.TempDir()
		err := os.WriteFile(filepath.Join(binDir, "bad.sh"), []byte("BAD"), 0744)
		require.NoError(t, err)

		v := NewImageVerifier(&Config{
			BinDir:             binDir,
			MaxVerifiers:       -1,
			PerVerifierTimeout: tomlext.FromStdTime(5 * time.Second),
		})

		j, err := v.VerifyImage(ctx, "registry.example.com/image:abc", ocispec.Descriptor{})
		assert.Error(t, err)
		assert.Nil(t, j)
	})

	t.Run("descriptor larger than linux pipe buffer, verifier doesn't read stdin", func(t *testing.T) {
		binDir := newBinDir(t, allBinsDir,
			"accept_reason_a",
		)

		v := NewImageVerifier(&Config{
			BinDir:             binDir,
			MaxVerifiers:       1,
			PerVerifierTimeout: tomlext.FromStdTime(5 * time.Second),
		})

		j, err := v.VerifyImage(ctx, "registry.example.com/image:abc", ocispec.Descriptor{
			Digest:    "sha256:98ea6e4f216f2fb4b69fff9b3a44842c38686ca685f3f55dc48c5d3fb1107be4",
			MediaType: "application/vnd.docker.distribution.manifest.list.v2+json",
			Size:      2048,
			Annotations: map[string]string{
				// Pipe buffer is usually 64KiB.
				"large_payload": strings.Repeat("0", 2*64*(1<<10)),
			},
		})

		// Should see a log like the following, but verification still succeeds:
		// time="2023-09-05T11:15:50-04:00" level=warning msg="failed to completely write descriptor to stdin" error="write |1: broken pipe"

		assert.NoError(t, err)
		assert.True(t, j.OK)
		assert.NotEmpty(t, j.Reason)
	})
}
