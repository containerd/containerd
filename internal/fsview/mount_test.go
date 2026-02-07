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

package fsview

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/internal/erofsutils"
	"github.com/containerd/containerd/v2/pkg/archive/tartest"
)

func TestFSMountsLast(t *testing.T) {
	tmp := t.TempDir()
	dir1 := filepath.Join(tmp, "dir1")
	dir2 := filepath.Join(tmp, "dir2")
	if err := os.Mkdir(dir1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir2, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir1, "f1"), []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "f2"), []byte("2"), 0644); err != nil {
		t.Fatal(err)
	}

	mounts := []mount.Mount{
		{Type: "bind", Source: dir1, Target: "/mnt/dir1"},
		{Type: "bind", Source: dir2, Target: "/mnt/dir2"},
	}

	// Should pick the last one (dir2)
	fs, err := FSMounts(mounts)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()

	if _, err := fs.Open("f2"); err != nil {
		t.Errorf("expected to find f2 in last mount, got %v", err)
	}
	if _, err := fs.Open("f1"); err == nil {
		t.Error("expected NOT to find f1 (should only have dir2)")
	}
}

func skipIfNoMkfsErofs(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("mkfs.erofs"); err != nil {
		t.Skip("mkfs.erofs not found in PATH")
	}
	supported, err := erofsutils.SupportGenerateFromTar()
	if err != nil {
		t.Skipf("failed to check mkfs.erofs tar support: %v", err)
	}
	if !supported {
		t.Skip("mkfs.erofs does not support --tar= mode")
	}
}

func makeEROFSFromTar(t *testing.T, wt tartest.WriterToTar) string {
	t.Helper()
	layerPath := filepath.Join(t.TempDir(), "layer.erofs")
	rc := tartest.TarFromWriterTo(wt)
	defer rc.Close()
	if err := erofsutils.ConvertTarErofs(context.Background(), rc, layerPath, "", nil); err != nil {
		t.Fatalf("ConvertTarErofs failed: %v", err)
	}
	return layerPath
}

func mergeEROFSLayers(t *testing.T, blobs ...string) string {
	t.Helper()
	output := filepath.Join(t.TempDir(), "merged.erofs")
	args := append([]string{"--aufs", "--ovlfs-strip=1", "--quiet", output}, blobs...)
	cmd := exec.Command("mkfs.erofs", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mkfs.erofs merge failed: %s: %v", string(out), err)
	}
	return output
}

// makeBaseErofs builds the base layer EROFS image (replaces testdata/base.erofs).
func makeBaseErofs(t *testing.T) string {
	t.Helper()
	skipIfNoMkfsErofs(t)
	tc := tartest.TarContext{}.WithModTime(time.Now().UTC())
	return makeEROFSFromTar(t, tartest.TarAll(
		tc.Dir("dir1", 0755),
		tc.File("dir1/file1.txt", []byte("file1 content\n"), 0644),
		tc.File("dir1/file2.txt", []byte("file2 content\n"), 0644),
		tc.Dir("dir2", 0755),
		tc.File("dir2/file2.txt", []byte("file2 content\n"), 0644),
		tc.Dir("dir2/subdir", 0755),
		tc.File("dir2/subdir/subfile.txt", []byte("subfile content\n"), 0644),
	))
}

// makeUpper1Erofs builds the first upper layer EROFS image (replaces testdata/upper1.erofs).
// Contains an opaque dir1 with a new file and whiteouts for file1.txt and file2.txt.
func makeUpper1Erofs(t *testing.T) string {
	t.Helper()
	skipIfNoMkfsErofs(t)
	tc := tartest.TarContext{}.WithModTime(time.Now().UTC())
	return makeEROFSFromTar(t, tartest.TarAll(
		tc.Dir("dir1", 0755),
		tc.File("dir1/.wh..wh..opq", []byte{}, 0644),
		tc.File("dir1/newfile.txt", []byte("new file content\n"), 0644),
		tc.File("dir1/.wh.file1.txt", []byte{}, 0644),
		tc.File("dir1/.wh.file2.txt", []byte{}, 0644),
		tc.Dir("dir2", 0755),
	))
}

// makeUpper2Erofs builds the second upper layer EROFS image (replaces testdata/upper2.erofs).
// Contains only a whiteout for dir1/file1.txt.
func makeUpper2Erofs(t *testing.T) string {
	t.Helper()
	skipIfNoMkfsErofs(t)
	tc := tartest.TarContext{}.WithModTime(time.Now().UTC())
	return makeEROFSFromTar(t, tartest.TarAll(
		tc.Dir("dir1", 0755),
		tc.File("dir1/.wh.file1.txt", []byte{}, 0644),
	))
}

func TestFSMountsEROFSWithDevices(t *testing.T) {
	skipIfNoMkfsErofs(t)

	tc := tartest.TarContext{}.WithModTime(time.Now().UTC())

	layer1Path := makeEROFSFromTar(t, tartest.TarAll(
		tc.Dir("dir", 0755),
		tc.File("dir/file1.txt", []byte("file1 content"), 0644),
		tc.File("dir/file2.txt", []byte("file2 content"), 0644),
	))

	layer2Path := makeEROFSFromTar(t, tartest.TarAll(
		tc.Dir("dir", 0755),
		tc.File("dir/file3.txt", []byte("file3 content"), 0644),
	))

	metaPath := mergeEROFSLayers(t, layer1Path, layer2Path)

	v, err := FSMounts([]mount.Mount{
		{
			Type:   "erofs",
			Source: metaPath,
			Options: []string{"ro", "device=" + layer1Path, "device=" + layer2Path},
		},
	})
	if err != nil {
		t.Fatalf("FSMounts failed: %v", err)
	}
	defer v.Close()

	for _, tt := range []struct {
		path    string
		content string
	}{
		{"dir/file1.txt", "file1 content"},
		{"dir/file2.txt", "file2 content"},
		{"dir/file3.txt", "file3 content"},
	} {
		f, err := v.Open(tt.path)
		if err != nil {
			t.Errorf("failed to open %s: %v", tt.path, err)
			continue
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			t.Errorf("failed to read %s: %v", tt.path, err)
			continue
		}
		if string(data) != tt.content {
			t.Errorf("%s: got %q, want %q", tt.path, string(data), tt.content)
		}
	}
}

func TestFSMountsOverlayAbsoluteSymlinkEtcGroup(t *testing.T) {
	skipIfNoMkfsErofs(t)

	expectedContent := "dummygroup:x:1001:root\n"
	tc := tartest.TarContext{}.WithModTime(time.Now().UTC())
	layerPath := makeEROFSFromTar(t, tartest.TarAll(
		tc.Dir("etc", 0o755),
		tc.Dir("nix", 0o755),
		tc.Dir("nix/store", 0o755),
		tc.Dir("nix/store/abcd", 0o755),
		tc.File("nix/store/abcd/group", []byte(expectedContent), 0o644),
		tc.Symlink("/nix/store/abcd/group", "etc/group"),
	))

	v, err := FSMounts([]mount.Mount{
		{
			Type:   "erofs",
			Source: layerPath,
		},
		{
			Type:   "format/overlay",
			Source: "overlay",
			Options: []string{
				"lowerdir={{ mount 0 }}",
			},
		},
	})
	if err != nil {
		t.Fatalf("FSMounts failed: %v", err)
	}
	defer v.Close()

	f, err := v.Open("etc/group")
	if err != nil {
		t.Fatalf("open etc/group through fsview failed: %v", err)
	}
	defer f.Close()

	content, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != expectedContent {
		t.Errorf("expected content %q, got %q", expectedContent, string(content))
	}
}

func TestFSMountsOverlayWithEROFSDevices(t *testing.T) {
	skipIfNoMkfsErofs(t)

	tc := tartest.TarContext{}.WithModTime(time.Now().UTC())

	// Layer 1 (bottom): regular files including ones that will be hidden
	layer1Path := makeEROFSFromTar(t, tartest.TarAll(
		tc.Dir("dir", 0755),
		tc.File("dir/file1.txt", []byte("file1 content"), 0644),
		tc.File("dir/file2.txt", []byte("file2 content"), 0644),
		tc.File("dir/file5.txt", []byte("file5 content"), 0644),
		tc.Dir("opaquedir", 0755),
		tc.File("opaquedir/old.txt", []byte("old content"), 0644),
	))

	// Layer 2 (top): new files, a whiteout for file5.txt, and an opaque opaquedir.
	// AUFS-style markers (.wh.*) are converted by mkfs.erofs --aufs to
	// overlay-native whiteouts (char dev 0:0) and opaque xattrs.
	layer2Path := makeEROFSFromTar(t, tartest.TarAll(
		tc.Dir("dir", 0755),
		tc.File("dir/file3.txt", []byte("file3 content"), 0644),
		tc.File("dir/.wh.file5.txt", []byte{}, 0644),
		tc.Dir("opaquedir", 0755),
		tc.File("opaquedir/.wh..wh..opq", []byte{}, 0644),
		tc.File("opaquedir/new.txt", []byte("new content"), 0644),
	))

	// Merge with layer1 as lower (first arg) and layer2 as upper (last arg)
	// so whiteouts in layer2 apply to layer1.
	metaPath := mergeEROFSLayers(t, layer1Path, layer2Path)

	// Create upper directory with additional and override files
	upperDir := filepath.Join(t.TempDir(), "upper")
	if err := os.MkdirAll(filepath.Join(upperDir, "dir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(upperDir, "dir", "file4.txt"), []byte("file4 content"), 0644); err != nil {
		t.Fatal(err)
	}
	// Override file1.txt from lower layer
	if err := os.WriteFile(filepath.Join(upperDir, "dir", "file1.txt"), []byte("file1 override"), 0644); err != nil {
		t.Fatal(err)
	}

	v, err := FSMounts([]mount.Mount{
		{
			Type:   "erofs",
			Source: metaPath,
			Options: []string{"ro", "device=" + layer1Path, "device=" + layer2Path},
		},
		{
			Type:   "format/overlay",
			Source: "overlay",
			Options: []string{
				"upperdir=" + upperDir,
				"lowerdir={{ mount 0 }}",
			},
		},
	})
	if err != nil {
		t.Fatalf("FSMounts failed: %v", err)
	}
	defer v.Close()

	// Verify readable files
	for _, tt := range []struct {
		path    string
		content string
	}{
		{"dir/file1.txt", "file1 override"},     // upper overrides lower
		{"dir/file2.txt", "file2 content"},       // from erofs layer 1
		{"dir/file3.txt", "file3 content"},       // from erofs layer 2
		{"dir/file4.txt", "file4 content"},       // from upper only
		{"opaquedir/new.txt", "new content"},     // from erofs layer 2 (survives opaque)
	} {
		f, err := v.Open(tt.path)
		if err != nil {
			t.Errorf("failed to open %s: %v", tt.path, err)
			continue
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			t.Errorf("failed to read %s: %v", tt.path, err)
			continue
		}
		if string(data) != tt.content {
			t.Errorf("%s: got %q, want %q", tt.path, string(data), tt.content)
		}
	}

	// Verify whiteout: file5.txt was in layer 1 but whited out by layer 2
	if _, err := v.Open("dir/file5.txt"); err == nil {
		t.Error("dir/file5.txt should not exist (whiteout)")
	}

	// Verify opaque: old.txt was in layer 1's opaquedir but hidden by opaque marker in layer 2
	if _, err := v.Open("opaquedir/old.txt"); err == nil {
		t.Error("opaquedir/old.txt should not exist (opaque directory)")
	}
}
