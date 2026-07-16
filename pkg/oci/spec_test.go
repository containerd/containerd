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

package oci

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/containerd/continuity/fs/fstest"
	"github.com/moby/sys/user"
	"github.com/opencontainers/runtime-spec/specs-go"
)

func TestGenerateSpec(t *testing.T) {
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "testing")
	s, err := GenerateSpec(ctx, nil, &containers.Container{ID: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		t.Fatal("GenerateSpec() returns a nil spec")
	}

	if runtime.GOOS == "linux" {
		// check for matching caps
		defaults := defaultUnixCaps()
		for _, cl := range [][]string{
			s.Process.Capabilities.Bounding,
			s.Process.Capabilities.Permitted,
			s.Process.Capabilities.Effective,
		} {
			for i := range defaults {
				if cl[i] != defaults[i] {
					t.Errorf("cap at %d does not match set %q != %q", i, defaults[i], cl[i])
				}
			}
		}

		// check default namespaces
		defaultNS := defaultUnixNamespaces()
		for i, ns := range s.Linux.Namespaces {
			if defaultNS[i] != ns {
				t.Errorf("ns at %d does not match set %q != %q", i, defaultNS[i], ns)
			}
		}
	} else if runtime.GOOS == "windows" {
		if s.Windows == nil {
			t.Fatal("Windows section of spec not filled in for Windows spec")
		}
	}

	// test that we don't have tty set
	if s.Process.Terminal {
		t.Error("terminal set on default process")
	}
}

func TestGenerateSpecWithPlatform(t *testing.T) {
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "testing")
	platforms := []string{"windows/amd64", "linux/amd64"}
	for _, p := range platforms {
		t.Logf("Testing platform: %s", p)
		s, err := GenerateSpecWithPlatform(ctx, nil, p, &containers.Container{ID: t.Name()})
		if err != nil {
			t.Fatalf("failed to generate spec: %v", err)
		}

		if s.Root == nil {
			t.Fatal("expected non nil Root section.")
		}
		if s.Process == nil {
			t.Fatal("expected non nil Process section.")
		}
		if p == "windows/amd64" {
			if s.Linux != nil {
				t.Fatal("expected nil Linux section")
			}
			if s.Windows == nil {
				t.Fatal("expected non nil Windows section")
			}
		} else {
			if s.Linux == nil {
				t.Fatal("expected non nil Linux section")
			}
			if runtime.GOOS == "windows" && s.Windows == nil {
				t.Fatal("expected non nil Windows section for LCOW")
			} else if runtime.GOOS != "windows" && s.Windows != nil {
				t.Fatal("expected nil Windows section")
			}
		}
	}
}

func TestSpecWithTTY(t *testing.T) {
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "testing")
	s, err := GenerateSpec(ctx, nil, &containers.Container{ID: t.Name()}, WithTTY)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Process.Terminal {
		t.Error("terminal net set WithTTY()")
	}
	if runtime.GOOS == "linux" {
		v := s.Process.Env[len(s.Process.Env)-1]
		if v != "TERM=xterm" {
			t.Errorf("xterm not set in env for TTY")
		}
	} else {
		if len(s.Process.Env) != 0 {
			t.Fatal("Windows process args should be empty by default")
		}
	}
}

func TestWithLinuxNamespace(t *testing.T) {
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "testing")
	replacedNS := specs.LinuxNamespace{Type: specs.NetworkNamespace, Path: "/var/run/netns/test"}

	var s *specs.Spec
	var err error
	if runtime.GOOS != "windows" {
		s, err = GenerateSpec(ctx, nil, &containers.Container{ID: t.Name()}, WithLinuxNamespace(replacedNS))
	} else {
		s, err = GenerateSpecWithPlatform(ctx, nil, "linux/amd64", &containers.Container{ID: t.Name()}, WithLinuxNamespace(replacedNS))
	}
	if err != nil {
		t.Fatal(err)
	}

	defaultNS := defaultUnixNamespaces()
	found := false
	for i, ns := range s.Linux.Namespaces {
		if ns == replacedNS && !found {
			found = true
			continue
		}
		if defaultNS[i] != ns {
			t.Errorf("ns at %d does not match set %q != %q", i, defaultNS[i], ns)
		}
	}
}

func TestWithCapabilities(t *testing.T) {
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "testing")

	opts := []SpecOpts{
		WithCapabilities([]string{"CAP_SYS_ADMIN"}),
	}
	var s *specs.Spec
	var err error
	if runtime.GOOS != "windows" {
		s, err = GenerateSpec(ctx, nil, &containers.Container{ID: t.Name()}, opts...)
	} else {
		s, err = GenerateSpecWithPlatform(ctx, nil, "linux/amd64", &containers.Container{ID: t.Name()}, opts...)
	}
	if err != nil {
		t.Fatal(err)
	}

	if len(s.Process.Capabilities.Bounding) != 1 || s.Process.Capabilities.Bounding[0] != "CAP_SYS_ADMIN" {
		t.Error("Unexpected capabilities set")
	}
	if len(s.Process.Capabilities.Effective) != 1 || s.Process.Capabilities.Effective[0] != "CAP_SYS_ADMIN" {
		t.Error("Unexpected capabilities set")
	}
	if len(s.Process.Capabilities.Permitted) != 1 || s.Process.Capabilities.Permitted[0] != "CAP_SYS_ADMIN" {
		t.Error("Unexpected capabilities set")
	}
	if len(s.Process.Capabilities.Inheritable) != 0 {
		t.Errorf("Unexpected capabilities set: length is non zero (%d)", len(s.Process.Capabilities.Inheritable))
	}
}

func TestWithCapabilitiesNil(t *testing.T) {
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "testing")

	s, err := GenerateSpec(ctx, nil, &containers.Container{ID: t.Name()},
		WithCapabilities(nil),
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(s.Process.Capabilities.Bounding) != 0 {
		t.Errorf("Unexpected capabilities set: length is non zero (%d)", len(s.Process.Capabilities.Bounding))
	}
	if len(s.Process.Capabilities.Effective) != 0 {
		t.Errorf("Unexpected capabilities set: length is non zero (%d)", len(s.Process.Capabilities.Effective))
	}
	if len(s.Process.Capabilities.Permitted) != 0 {
		t.Errorf("Unexpected capabilities set: length is non zero (%d)", len(s.Process.Capabilities.Permitted))
	}
	if len(s.Process.Capabilities.Inheritable) != 0 {
		t.Errorf("Unexpected capabilities set: length is non zero (%d)", len(s.Process.Capabilities.Inheritable))
	}
}

func TestPopulateDefaultWindowsSpec(t *testing.T) {
	var (
		c   = containers.Container{ID: "TestWithDefaultSpec"}
		ctx = namespaces.WithNamespace(context.Background(), "test")
	)
	var expected Spec

	populateDefaultWindowsSpec(ctx, &expected, c.ID)
	if expected.Windows == nil {
		t.Error("Cannot populate windows Spec")
	}
}

func TestPopulateDefaultUnixSpec(t *testing.T) {
	var (
		c   = containers.Container{ID: "TestWithDefaultSpec"}
		ctx = namespaces.WithNamespace(context.Background(), "test")
	)
	var expected Spec

	populateDefaultUnixSpec(ctx, &expected, c.ID)
	if expected.Linux == nil {
		t.Error("Cannot populate Unix Spec")
	}

	// CgroupsPath should never contain backslashes when the spec is generated
	// on Windows.
	if strings.Contains(expected.Linux.CgroupsPath, "\\") {
		t.Errorf("CgroupsPath contains backslashes: %s", expected.Linux.CgroupsPath)
	}
}

func TestWithPrivileged(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "linux" {
		// because WithPrivileged depends on CapEff in /proc/self/status
		testutil.RequiresRoot(t)
	}

	ctx := namespaces.WithNamespace(context.Background(), "testing")

	opts := []SpecOpts{
		WithCapabilities(nil),
		WithMounts([]specs.Mount{
			{Type: "cgroup", Destination: "/sys/fs/cgroup", Options: []string{"ro"}},
		}),
		WithPrivileged,
	}
	var s *specs.Spec
	var err error
	if runtime.GOOS != "windows" {
		s, err = GenerateSpec(ctx, nil, &containers.Container{ID: t.Name()}, opts...)
	} else {
		s, err = GenerateSpecWithPlatform(ctx, nil, "linux/amd64", &containers.Container{ID: t.Name()}, opts...)
	}
	if err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS != "linux" {
		return
	}

	if len(s.Process.Capabilities.Bounding) == 0 {
		t.Error("Expected capabilities to be set with privileged")
	}

	var foundSys, foundCgroup bool
	for _, m := range s.Mounts {
		switch m.Type {
		case "sysfs":
			foundSys = true
			var found bool
			for _, o := range m.Options {
				switch o {
				case "ro":
					t.Errorf("Found unexpected read only %s mount", m.Type)
				case "rw":
					found = true
				}
			}
			if !found {
				t.Errorf("Did not find rw mount option for %s", m.Type)
			}
		case "cgroup":
			foundCgroup = true
			var found bool
			for _, o := range m.Options {
				switch o {
				case "ro":
					t.Errorf("Found unexpected read only %s mount", m.Type)
				case "rw":
					found = true
				}
			}
			if !found {
				t.Errorf("Did not find rw mount option for %s", m.Type)
			}
		}
	}
	if !foundSys {
		t.Error("Did not find mount for sysfs")
	}
	if !foundCgroup {
		t.Error("Did not find mount for cgroupfs")
	}
}

func TestOpenUserFile_AbsoluteSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("absolute symlink handling is only supported on non-Windows platforms")
	}

	expectedContent := []byte("root:x:0:0:root:/root:/bin/bash" + t.Name())

	root := t.TempDir()
	// Use 'continuity' library to create a directory structure simulating NixOS
	if err := fstest.Apply(
		fstest.CreateDir("/etc", 0o755),
		fstest.CreateDir("/nix/store/abcd", 0o755),
		fstest.CreateFile("/nix/store/abcd/passwd", expectedContent, 0o644),
		// /etc/passwd -> /nix/store/abcd/passwd (absolute symlink)
		fstest.Symlink("/nix/store/abcd/passwd", "/etc/passwd"),
	).Apply(root); err != nil {
		t.Fatal(err)
	}

	rootFS := os.DirFS(root)

	// Ensure the FS implements the ReadLink interface.
	// If the native os.DirFS doesn't implement it (depending on Go version),
	// wrap it in our readLinkFS helper.
	if _, ok := rootFS.(readLinker); !ok {
		t.Logf("os.DirFS does not implement ReadLink; wrapping to use ReadLink")
		rootFS = readLinkFS{root: root, fs: rootFS}
	}

	f, err := openUserFile(rootFS, "etc/passwd")
	if err != nil {
		t.Fatalf("openUserFile failed on absolute symlink: %v", err)
	}
	defer f.Close()

	content, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(expectedContent) {
		t.Errorf("expected content %q, got %q", string(expectedContent), string(content))
	}
}

func TestGroupLookup_AbsoluteSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("absolute symlink handling is only supported on non-Windows platforms")
	}

	expectedContent := []byte("dummygroup:x:1001:paulo\n")

	root := t.TempDir()
	if err := fstest.Apply(
		fstest.CreateDir("/etc", 0o755),
		fstest.CreateDir("/nix/store/abcd", 0o755),
		fstest.CreateFile("/nix/store/abcd/group", expectedContent, 0o644),
		fstest.Symlink("/nix/store/abcd/group", "/etc/group"),
	).Apply(root); err != nil {
		t.Fatal(err)
	}

	rootFS := os.DirFS(root)
	if _, ok := rootFS.(readLinker); !ok {
		rootFS = readLinkFS{root: root, fs: rootFS}
	}

	gid, err := GIDFromFS(rootFS, func(g user.Group) bool {
		return g.Name == "dummygroup"
	})
	if err != nil {
		t.Fatalf("GIDFromFS failed on absolute symlink: %v", err)
	}
	if gid != 1001 {
		t.Errorf("expected GID 1001, got %d", gid)
	}

	gids, err := getSupplementalGroupsFromFS(rootFS, func(g user.Group) bool {
		return g.Name == "dummygroup"
	})
	if err != nil {
		t.Fatalf("getSupplementalGroupsFromFS failed on absolute symlink: %v", err)
	}
	if len(gids) != 1 || gids[0] != 1001 {
		t.Errorf("expected supplemental GIDs [1001], got %v", gids)
	}
}

// TestOpenUserFile_IntermediateDirSymlink tests that openUserFile resolves
// absolute symlinks on intermediate directory components, not just the final
// file. This covers rootfs layouts like Android emulator images where /etc is
// an absolute symlink to /system/etc, so etc/passwd is only reachable via
// the chain etc -> /system/etc -> system/etc/passwd.
func TestOpenUserFile_IntermediateDirSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}

	expectedContent := []byte("root:x:0:0:root:/root:/bin/sh\n")

	root := t.TempDir()
	if err := fstest.Apply(
		fstest.CreateDir("/system", 0o755),
		fstest.CreateDir("/system/etc", 0o755),
		fstest.CreateFile("/system/etc/passwd", expectedContent, 0o644),
		fstest.CreateFile("/system/etc/group", []byte("root:x:0:\n"), 0o644),
		// /etc -> /system/etc  (absolute symlink to a directory)
		fstest.Symlink("/system/etc", "/etc"),
	).Apply(root); err != nil {
		t.Fatal(err)
	}

	wrapFS := func(base string) fs.FS {
		plain := os.DirFS(base)
		if _, ok := plain.(readLinker); ok {
			return plain
		}
		return readLinkFS{root: base, fs: plain}
	}

	t.Run("passwd via absolute dir symlink", func(t *testing.T) {
		f, err := openUserFile(wrapFS(root), "etc/passwd")
		if err != nil {
			t.Fatalf("openUserFile failed for etc/passwd through absolute dir symlink: %v", err)
		}
		defer f.Close()
		got, readErr := io.ReadAll(f)
		if readErr != nil {
			t.Fatalf("io.ReadAll failed: %v", readErr)
		}
		if string(got) != string(expectedContent) {
			t.Errorf("content mismatch: got %q, want %q", got, expectedContent)
		}
	})

	t.Run("group via absolute dir symlink", func(t *testing.T) {
		f, err := openUserFile(wrapFS(root), "etc/group")
		if err != nil {
			t.Fatalf("openUserFile failed for etc/group through absolute dir symlink: %v", err)
		}
		defer f.Close()
	})

}

// TestOpenUserFile_RelativeDirSymlink tests that openUserFile resolves
// relative symlinks on intermediate directory components.
func TestOpenUserFile_RelativeDirSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}

	expectedContent := []byte("root:x:0:0:root:/root:/bin/sh\n")

	root := t.TempDir()
	if err := fstest.Apply(
		fstest.CreateDir("/real_etc", 0o755),
		fstest.CreateFile("/real_etc/passwd", expectedContent, 0o644),
		// /etc -> real_etc  (relative symlink to a directory)
		fstest.Symlink("real_etc", "/etc"),
	).Apply(root); err != nil {
		t.Fatal(err)
	}

	wrapFS := func(base string) fs.FS {
		plain := os.DirFS(base)
		if _, ok := plain.(readLinker); ok {
			return plain
		}
		return readLinkFS{root: base, fs: plain}
	}

	f, err := openUserFile(wrapFS(root), "etc/passwd")
	if err != nil {
		t.Fatalf("openUserFile failed for etc/passwd through relative dir symlink: %v", err)
	}
	defer f.Close()
	got, readErr := io.ReadAll(f)
	if readErr != nil {
		t.Fatalf("io.ReadAll failed: %v", readErr)
	}
	if string(got) != string(expectedContent) {
		t.Errorf("content mismatch: got %q, want %q", got, expectedContent)
	}
}

// Helpers for testing ReadLink support
type readLinkFS struct {
	root string
	fs   fs.FS
}

func (r readLinkFS) Open(name string) (fs.File, error) {
	return r.fs.Open(name)
}

func (r readLinkFS) ReadLink(name string) (string, error) {
	// Force link reading using the actual path on disk
	return os.Readlink(filepath.Join(r.root, filepath.FromSlash(name)))
}
