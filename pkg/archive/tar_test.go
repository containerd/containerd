//go:build !windows

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

package archive

import (
	"archive/tar"
	"bytes"
	"context"
	_ "crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/pkg/archive/tartest"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/continuity/fs/fstest"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

const tarCmd = "tar"

// baseApplier creates a basic filesystem layout
// with multiple types of files for basic tests.
var baseApplier = fstest.Apply(
	fstest.CreateDir("/etc/", 0755),
	fstest.CreateFile("/etc/hosts", []byte("127.0.0.1 localhost"), 0644),
	fstest.Link("/etc/hosts", "/etc/hosts.allow"),
	fstest.CreateDir("/usr/local/lib", 0755),
	fstest.CreateFile("/usr/local/lib/libnothing.so", []byte{0x00, 0x00}, 0755),
	fstest.Symlink("libnothing.so", "/usr/local/lib/libnothing.so.2"),
	fstest.CreateDir("/home", 0755),
	fstest.CreateDir("/home/derek", 0700),
)

func TestUnpack(t *testing.T) {
	requireTar(t)

	if err := testApply(t, baseApplier); err != nil {
		t.Fatalf("Test apply failed: %+v", err)
	}
}

func TestBaseDiff(t *testing.T) {
	requireTar(t)

	if err := testBaseDiff(t, baseApplier); err != nil {
		t.Fatalf("Test base diff failed: %+v", err)
	}
}

func TestRelativeSymlinks(t *testing.T) {
	breakoutLinks := []fstest.Applier{
		fstest.Apply(
			baseApplier,
			fstest.Symlink("../other", "/home/derek/other"),
			fstest.Symlink("../../etc", "/home/derek/etc"),
			fstest.Symlink("up/../../other", "/home/derek/updown"),
		),
		fstest.Apply(
			baseApplier,
			fstest.Symlink("../../../breakout", "/home/derek/breakout"),
		),
		fstest.Apply(
			baseApplier,
			fstest.Symlink("../../breakout", "/breakout"),
		),
		fstest.Apply(
			baseApplier,
			fstest.Symlink("etc/../../upandout", "/breakout"),
		),
		fstest.Apply(
			baseApplier,
			fstest.Symlink("derek/../../../downandout", "/home/breakout"),
		),
		fstest.Apply(
			baseApplier,
			fstest.Symlink("/etc", "localetc"),
		),
	}

	for _, bo := range breakoutLinks {
		if err := testDiffApply(t, bo); err != nil {
			t.Fatalf("Test apply failed: %+v", err)
		}
	}
}

func TestSymlinks(t *testing.T) {
	links := [][2]fstest.Applier{
		{
			fstest.Apply(
				fstest.CreateDir("/bin/", 0755),
				fstest.CreateFile("/bin/superbinary", []byte{0x00, 0x00}, 0755),
				fstest.Symlink("../bin/superbinary", "/bin/other1"),
			),
			fstest.Apply(
				fstest.Remove("/bin/other1"),
				fstest.Symlink("/bin/superbinary", "/bin/other1"),
				fstest.Symlink("../bin/superbinary", "/bin/other2"),
				fstest.Symlink("superbinary", "/bin/other3"),
			),
		},
		{
			fstest.Apply(
				fstest.CreateDir("/bin/", 0755),
				fstest.CreateDir("/sbin/", 0755),
				fstest.CreateFile("/sbin/superbinary", []byte{0x00, 0x00}, 0755),
				fstest.Symlink("/sbin/superbinary", "/bin/superbinary"),
				fstest.Symlink("../bin/superbinary", "/bin/other1"),
			),
			fstest.Apply(
				fstest.Remove("/bin/other1"),
				fstest.Symlink("/bin/superbinary", "/bin/other1"),
				fstest.Symlink("superbinary", "/bin/other2"),
			),
		},
		{
			fstest.Apply(
				fstest.CreateDir("/bin/", 0755),
				fstest.CreateDir("/sbin/", 0755),
				fstest.CreateFile("/sbin/superbinary", []byte{0x00, 0x00}, 0755),
				fstest.Symlink("../sbin/superbinary", "/bin/superbinary"),
				fstest.Symlink("../bin/superbinary", "/bin/other1"),
			),
			fstest.Apply(
				fstest.Remove("/bin/other1"),
				fstest.Symlink("/bin/superbinary", "/bin/other1"),
			),
		},
		{
			fstest.Apply(
				fstest.CreateDir("/bin/", 0755),
				fstest.CreateFile("/bin/actualbinary", []byte{0x00, 0x00}, 0755),
				fstest.Symlink("actualbinary", "/bin/superbinary"),
				fstest.Symlink("../bin/superbinary", "/bin/other1"),
				fstest.Symlink("superbinary", "/bin/other2"),
			),
			fstest.Apply(
				fstest.Remove("/bin/other1"),
				fstest.Remove("/bin/other2"),
				fstest.Symlink("/bin/superbinary", "/bin/other1"),
				fstest.Symlink("superbinary", "/bin/other2"),
			),
		},
		{
			fstest.Apply(
				fstest.CreateDir("/bin/", 0755),
				fstest.CreateFile("/bin/actualbinary", []byte{0x00, 0x00}, 0755),
				fstest.Symlink("actualbinary", "/bin/myapp"),
			),
			fstest.Apply(
				fstest.Remove("/bin/myapp"),
				fstest.CreateDir("/bin/myapp", 0755),
			),
		},
	}

	for i, l := range links {
		if err := testDiffApply(t, l[0], l[1]); err != nil {
			t.Fatalf("Test[%d] apply failed: %+v", i+1, err)
		}
	}
}

func TestTarWithXattr(t *testing.T) {
	testutil.RequiresRoot(t)

	fileXattrExist := func(f1, xattrKey, xattrValue string) func(string) error {
		return func(root string) error {
			values, err := getxattr(filepath.Join(root, f1), xattrKey)
			if err != nil {
				return err
			}
			if xattrValue != string(values) {
				return fmt.Errorf("file xattrs expect to be %s, actually get %s", xattrValue, values)
			}
			return nil
		}
	}

	tests := []struct {
		name  string
		key   string
		value string
		err   error
	}{
		{
			name:  "WithXattrsUser",
			key:   "user.key",
			value: "value",
		},
		{
			// security related xattrs need root permission to test
			name:  "WithXattrSelinux",
			key:   "security.selinux",
			value: "unconfined_u:object_r:default_t:s0\x00",
		},
	}
	for _, at := range tests {
		tc := tartest.TarContext{}.WithUIDGID(os.Getuid(), os.Getgid()).WithModTime(time.Now().UTC()).WithXattrs(map[string]string{
			at.key: at.value,
		})
		w := tartest.TarAll(tc.File("/file", []byte{}, 0755))
		validator := fileXattrExist("file", at.key, at.value)
		t.Run(at.name, makeWriterToTarTest(w, nil, validator, at.err))
	}
}

func TestBreakouts(t *testing.T) {
	tc := tartest.TarContext{}.WithUIDGID(os.Getuid(), os.Getgid()).WithModTime(time.Now().UTC())
	expected := "unbroken"
	unbrokenCheck := func(root string) error {
		b, err := os.ReadFile(filepath.Join(root, "etc", "unbroken"))
		if err != nil {
			return fmt.Errorf("failed to read unbroken: %w", err)
		}
		if string(b) != expected {
			return fmt.Errorf("/etc/unbroken: unexpected value %s, expected %s", b, expected)
		}
		return nil
	}
	errFileDiff := errors.New("files differ")
	td := t.TempDir()

	isSymlinkFile := func(f string) func(string) error {
		return func(root string) error {
			fi, err := os.Lstat(filepath.Join(root, f))
			if err != nil {
				return err
			}

			if got := fi.Mode() & os.ModeSymlink; got != os.ModeSymlink {
				return fmt.Errorf("%s should be symlink", fi.Name())
			}
			return nil
		}
	}

	sameSymlinkFile := func(f1, f2 string) func(string) error {
		checkF1, checkF2 := isSymlinkFile(f1), isSymlinkFile(f2)
		return func(root string) error {
			if err := checkF1(root); err != nil {
				return err
			}

			if err := checkF2(root); err != nil {
				return err
			}

			t1, err := os.Readlink(filepath.Join(root, f1))
			if err != nil {
				return err
			}

			t2, err := os.Readlink(filepath.Join(root, f2))
			if err != nil {
				return err
			}

			if t1 != t2 {
				return fmt.Errorf("%#v and %#v: %w", t1, t2, errFileDiff)
			}
			return nil
		}
	}

	sameFile := func(f1, f2 string) func(string) error {
		return func(root string) error {
			p1, err := fs.RootPath(root, f1)
			if err != nil {
				return err
			}
			p2, err := fs.RootPath(root, f2)
			if err != nil {
				return err
			}
			s1, err := os.Stat(p1)
			if err != nil {
				return err
			}
			s2, err := os.Stat(p2)
			if err != nil {
				return err
			}
			if !os.SameFile(s1, s2) {
				return fmt.Errorf("%#v and %#v: %w", s1, s2, errFileDiff)
			}
			return nil
		}
	}
	notSameFile := func(f1, f2 string) func(string) error {
		same := sameFile(f1, f2)
		return func(root string) error {
			err := same(root)
			if err == nil {
				return errors.New("files are the same, expected diff")
			}
			if !errors.Is(err, errFileDiff) {
				return err
			}
			return nil
		}
	}
	fileValue := func(f1 string, content []byte) func(string) error {
		return func(root string) error {
			b, err := os.ReadFile(filepath.Join(root, f1))
			if err != nil {
				return err
			}
			if !bytes.Equal(b, content) {
				return fmt.Errorf("content differs: expected %v, got %v", content, b)
			}
			return nil
		}
	}
	fileNotExists := func(f1 string) func(string) error {
		return func(root string) error {
			_, err := os.Lstat(filepath.Join(root, f1))
			if err == nil {
				return errors.New("file exists")
			} else if !os.IsNotExist(err) {
				return err
			}
			return nil
		}

	}
	all := func(funcs ...func(string) error) func(string) error {
		return func(root string) error {
			for _, f := range funcs {
				if err := f(root); err != nil {
					return err
				}
			}
			return nil
		}
	}

	type breakoutTest struct {
		name      string
		w         tartest.WriterToTar
		apply     fstest.Applier
		validator func(string) error
		err       error
	}
	breakouts := []breakoutTest{
		{
			name: "SymlinkAbsolute",
			w: tartest.TarAll(
				tc.Dir("etc", 0755),
				tc.Symlink("/etc", "localetc"),
				tc.File("/localetc/unbroken", []byte(expected), 0644),
			),
			validator: unbrokenCheck,
		},
		{
			name: "SymlinkUpAndOut",
			w: tartest.TarAll(
				tc.Dir("etc", 0755),
				tc.Dir("dummy", 0755),
				tc.Symlink("/dummy/../etc", "localetc"),
				tc.File("/localetc/unbroken", []byte(expected), 0644),
			),
			validator: unbrokenCheck,
		},
		{
			name: "SymlinkMultipleAbsolute",
			w: tartest.TarAll(
				tc.Dir("etc", 0755),
				tc.Dir("dummy", 0755),
				tc.Symlink("/etc", "/dummy/etc"),
				tc.Symlink("/dummy/etc", "localetc"),
				tc.File("/dummy/etc/unbroken", []byte(expected), 0644),
			),
			validator: unbrokenCheck,
		},
		{
			name: "SymlinkMultipleRelative",
			w: tartest.TarAll(
				tc.Dir("etc", 0755),
				tc.Dir("dummy", 0755),
				tc.Symlink("/etc", "/dummy/etc"),
				tc.Symlink("./dummy/etc", "localetc"),
				tc.File("/dummy/etc/unbroken", []byte(expected), 0644),
			),
			validator: unbrokenCheck,
		},
		{
			name: "SymlinkEmptyFile",
			w: tartest.TarAll(
				tc.Dir("etc", 0755),
				tc.File("etc/emptied", []byte("notempty"), 0644),
				tc.Symlink("/etc", "localetc"),
				tc.File("/localetc/emptied", []byte{}, 0644),
			),
			validator: func(root string) error {
				b, err := os.ReadFile(filepath.Join(root, "etc", "emptied"))
				if err != nil {
					return fmt.Errorf("failed to read unbroken: %w", err)
				}
				if len(b) > 0 {
					return errors.New("/etc/emptied: non-empty")
				}
				return nil
			},
		},
		{
			name: "HardlinkRelative",
			w: tartest.TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Dir("breakouts", 0755),
				tc.Symlink("../../etc", "breakouts/d1"),
				tc.Link("/breakouts/d1/passwd", "breakouts/mypasswd"),
			),
			validator: sameFile("/breakouts/mypasswd", "/etc/passwd"),
		},
		{
			name: "HardlinkDownAndOut",
			w: tartest.TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Dir("breakouts", 0755),
				tc.Dir("downandout", 0755),
				tc.Symlink("../downandout/../../etc", "breakouts/d1"),
				tc.Link("/breakouts/d1/passwd", "breakouts/mypasswd"),
			),
			validator: sameFile("/breakouts/mypasswd", "/etc/passwd"),
		},
		{
			name: "HardlinkAbsolute",
			w: tartest.TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Symlink("/etc", "localetc"),
				tc.Link("/localetc/passwd", "localpasswd"),
			),
			validator: sameFile("localpasswd", "/etc/passwd"),
		},
		{
			name: "HardlinkRelativeLong",
			w: tartest.TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Symlink("../../../../../../../etc", "localetc"),
				tc.Link("/localetc/passwd", "localpasswd"),
			),
			validator: sameFile("localpasswd", "/etc/passwd"),
		},
		{
			name: "HardlinkRelativeUpAndOut",
			w: tartest.TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Symlink("upandout/../../../etc", "localetc"),
				tc.Link("/localetc/passwd", "localpasswd"),
			),
			validator: sameFile("localpasswd", "/etc/passwd"),
		},
		{
			name: "HardlinkDirectRelative",
			w: tartest.TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Link("../../../../../etc/passwd", "localpasswd"),
			),
			validator: sameFile("localpasswd", "/etc/passwd"),
		},
		{
			name: "HardlinkDirectAbsolute",
			w: tartest.TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Link("/etc/passwd", "localpasswd"),
			),
			validator: sameFile("localpasswd", "/etc/passwd"),
		},

		{
			name: "SymlinkParentDirectory",
			w: tartest.TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Symlink("/etc/", ".."),
				tc.Link("/etc/passwd", "localpasswd"),
			),
			validator: sameFile("/localpasswd", "/etc/passwd"),
		},
		{
			name: "SymlinkEmptyFilename",
			w: tartest.TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Symlink("/etc/", ""),
				tc.Link("/etc/passwd", "localpasswd"),
			),
			validator: sameFile("/localpasswd", "/etc/passwd"),
		},
		{
			name: "SymlinkParentRelative",
			w: tartest.TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Symlink("/etc/", "localetc/sub/.."),
				tc.Link("/etc/passwd", "/localetc/localpasswd"),
			),
			validator: sameFile("/localetc/localpasswd", "/etc/passwd"),
		},
		{
			name: "SymlinkSlashEnded",
			w: tartest.TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Dir("localetc/", 0770),
				tc.Link("/etc/passwd", "/localetc/localpasswd"),
			),
			validator: sameFile("/localetc/localpasswd", "/etc/passwd"),
		},
		{
			name: "SymlinkOverrideDirectory",
			apply: fstest.Apply(
				fstest.CreateDir("/etc/", 0755),
				fstest.CreateFile("/etc/passwd", []byte("inside"), 0644),
				fstest.CreateDir("/localetc/", 0755),
			),
			w: tartest.TarAll(
				tc.Symlink("/etc", "localetc"),
				tc.Link("/etc/passwd", "/localetc/localpasswd"),
			),
			validator: sameFile("/localetc/localpasswd", "/etc/passwd"),
		},
		{
			name: "SymlinkOverrideDirectoryRelative",
			apply: fstest.Apply(
				fstest.CreateDir("/etc/", 0755),
				fstest.CreateFile("/etc/passwd", []byte("inside"), 0644),
				fstest.CreateDir("/localetc/", 0755),
			),
			w: tartest.TarAll(
				tc.Symlink("../../etc", "localetc"),
				tc.Link("/etc/passwd", "/localetc/localpasswd"),
			),
			validator: sameFile("/localetc/localpasswd", "/etc/passwd"),
		},
		{
			name: "DirectoryOverrideSymlink",
			apply: fstest.Apply(
				fstest.CreateDir("/etc/", 0755),
				fstest.CreateFile("/etc/passwd", []byte("inside"), 0644),
				fstest.Symlink("/etc", "localetc"),
			),
			w: tartest.TarAll(
				tc.Dir("/localetc/", 0755),
				tc.Link("/etc/passwd", "/localetc/localpasswd"),
			),
			validator: sameFile("/localetc/localpasswd", "/etc/passwd"),
		},
		{
			name: "DirectoryOverrideSymlinkAndHardlink",
			apply: fstest.Apply(
				fstest.CreateDir("/etc/", 0755),
				fstest.CreateFile("/etc/passwd", []byte("inside"), 0644),
				fstest.Symlink("etc", "localetc"),
				fstest.Link("/etc/passwd", "/localetc/localpasswd"),
			),
			w: tartest.TarAll(
				tc.Dir("/localetc/", 0755),
				tc.File("/localetc/localpasswd", []byte("different"), 0644),
			),
			validator: notSameFile("/localetc/localpasswd", "/etc/passwd"),
		},
		{
			name: "WhiteoutRootParent",
			apply: fstest.Apply(
				fstest.CreateDir("/etc/", 0755),
				fstest.CreateFile("/etc/passwd", []byte("inside"), 0644),
			),
			w: tartest.TarAll(
				tc.File(".wh...", []byte{}, 0644), // Should wipe out whole directory
			),
			err: errInvalidArchive,
		},
		{
			name: "WhiteoutParent",
			apply: fstest.Apply(
				fstest.CreateDir("/etc/", 0755),
				fstest.CreateFile("/etc/passwd", []byte("inside"), 0644),
			),
			w: tartest.TarAll(
				tc.File("etc/.wh...", []byte{}, 0644),
			),
			err: errInvalidArchive,
		},
		{
			name: "WhiteoutRoot",
			apply: fstest.Apply(
				fstest.CreateDir("/etc/", 0755),
				fstest.CreateFile("/etc/passwd", []byte("inside"), 0644),
			),
			w: tartest.TarAll(
				tc.File(".wh..", []byte{}, 0644),
			),
			err: errInvalidArchive,
		},
		{
			name: "WhiteoutCurrentDirectory",
			apply: fstest.Apply(
				fstest.CreateDir("/etc/", 0755),
				fstest.CreateFile("/etc/passwd", []byte("inside"), 0644),
			),
			w: tartest.TarAll(
				tc.File("etc/.wh..", []byte{}, 0644), // Should wipe out whole directory
			),
			err: errInvalidArchive,
		},
		{
			name: "WhiteoutSymlink",
			apply: fstest.Apply(
				fstest.CreateDir("/etc/", 0755),
				fstest.CreateFile("/etc/passwd", []byte("all users"), 0644),
				fstest.Symlink("/etc", "localetc"),
			),
			w: tartest.TarAll(
				tc.File(".wh.localetc", []byte{}, 0644), // Should wipe out whole directory
			),
			validator: all(
				fileValue("etc/passwd", []byte("all users")),
				fileNotExists("localetc"),
			),
		},
		{
			// TODO: This test should change once archive apply is disallowing
			// symlinks as parents in the name
			name: "WhiteoutSymlinkPath",
			apply: fstest.Apply(
				fstest.CreateDir("/etc/", 0755),
				fstest.CreateFile("/etc/passwd", []byte("all users"), 0644),
				fstest.CreateFile("/etc/whitedout", []byte("ahhhh whiteout"), 0644),
				fstest.Symlink("/etc", "localetc"),
			),
			w: tartest.TarAll(
				tc.File("localetc/.wh.whitedout", []byte{}, 0644),
			),
			validator: all(
				fileValue("etc/passwd", []byte("all users")),
				fileNotExists("etc/whitedout"),
			),
		},
		{
			name: "WhiteoutDirectoryName",
			apply: fstest.Apply(
				fstest.CreateDir("/etc/", 0755),
				fstest.CreateFile("/etc/passwd", []byte("all users"), 0644),
				fstest.CreateFile("/etc/whitedout", []byte("ahhhh whiteout"), 0644),
				fstest.Symlink("/etc", "localetc"),
			),
			w: tartest.TarAll(
				tc.File(".wh.etc/somefile", []byte("non-empty"), 0644),
			),
			validator: all(
				fileValue("etc/passwd", []byte("all users")),
				fileValue(".wh.etc/somefile", []byte("non-empty")),
			),
		},
		{
			name: "WhiteoutDeadSymlinkParent",
			apply: fstest.Apply(
				fstest.CreateDir("/etc/", 0755),
				fstest.CreateFile("/etc/passwd", []byte("all users"), 0644),
				fstest.Symlink("/dne", "localetc"),
			),
			w: tartest.TarAll(
				tc.File("localetc/.wh.etc", []byte{}, 0644),
			),
			// no-op, remove does not
			validator: fileValue("etc/passwd", []byte("all users")),
		},
		{
			name: "WhiteoutRelativePath",
			apply: fstest.Apply(
				fstest.CreateDir("/etc/", 0755),
				fstest.CreateFile("/etc/passwd", []byte("all users"), 0644),
				fstest.Symlink("/dne", "localetc"),
			),
			w: tartest.TarAll(
				tc.File("dne/../.wh.etc", []byte{}, 0644),
			),
			// resolution ends up just removing etc
			validator: fileNotExists("etc/passwd"),
		},
	}

	// The follow tests perform operations not permitted on Darwin
	if runtime.GOOS != "darwin" {
		breakouts = append(breakouts, []breakoutTest{
			{
				name: "HardlinkSymlinkBeforeCreateTarget",
				w: tartest.TarAll(
					tc.Dir("etc", 0770),
					tc.Symlink("/etc/passwd", "localpasswd"),
					tc.Link("localpasswd", "localpasswd-dup"),
					tc.File("/etc/passwd", []byte("after"), 0644),
				),
				validator: sameFile("localpasswd-dup", "/etc/passwd"),
			},
			{
				name: "HardlinkSymlinkRelative",
				w: tartest.TarAll(
					tc.Dir("etc", 0770),
					tc.File("/etc/passwd", []byte("inside"), 0644),
					tc.Symlink("../../../../../etc/passwd", "passwdlink"),
					tc.Link("/passwdlink", "localpasswd"),
				),
				validator: all(
					sameSymlinkFile("/localpasswd", "/passwdlink"),
					sameFile("/localpasswd", "/etc/passwd"),
				),
			},
			{
				name: "HardlinkSymlinkAbsolute",
				w: tartest.TarAll(
					tc.Dir("etc", 0770),
					tc.File("/etc/passwd", []byte("inside"), 0644),
					tc.Symlink("/etc/passwd", "passwdlink"),
					tc.Link("/passwdlink", "localpasswd"),
				),
				validator: all(
					sameSymlinkFile("/localpasswd", "/passwdlink"),
					sameFile("/localpasswd", "/etc/passwd"),
				),
			},
			{
				name: "HardlinkSymlinkChmod",
				w: func() tartest.WriterToTar {
					p := filepath.Join(td, "perm400")
					if err := os.WriteFile(p, []byte("..."), 0400); err != nil {
						t.Fatal(err)
					}
					ep := filepath.Join(td, "also-exists-outside-root")
					if err := os.WriteFile(ep, []byte("..."), 0640); err != nil {
						t.Fatal(err)
					}

					return tartest.TarAll(
						tc.Symlink(p, ep),
						tc.Link(ep, "sketchylink"),
					)
				}(),
				validator: func(string) error {
					p := filepath.Join(td, "perm400")
					fi, err := os.Lstat(p)
					if err != nil {
						return err
					}
					if perm := fi.Mode() & os.ModePerm; perm != 0400 {
						return fmt.Errorf("%s perm changed from 0400 to %04o", p, perm)
					}
					return nil
				},
			},
		}...)
	}

	for _, bo := range breakouts {
		t.Run(bo.name, makeWriterToTarTest(bo.w, bo.apply, bo.validator, bo.err))
	}
}

func TestDiffApply(t *testing.T) {
	fstest.FSSuite(t, diffApplier{})
}

func TestApplyTar(t *testing.T) {
	tc := tartest.TarContext{}.WithUIDGID(os.Getuid(), os.Getgid()).WithModTime(time.Now().UTC())
	directoriesExist := func(dirs ...string) func(string) error {
		return func(root string) error {
			for _, d := range dirs {
				p, err := fs.RootPath(root, d)
				if err != nil {
					return err
				}
				if _, err := os.Stat(p); err != nil {
					return fmt.Errorf("failure checking existence for %v: %w", d, err)
				}
			}
			return nil
		}
	}

	tests := []struct {
		name      string
		w         tartest.WriterToTar
		apply     fstest.Applier
		validator func(string) error
		err       error
	}{
		{
			name: "DirectoryCreation",
			apply: fstest.Apply(
				fstest.CreateDir("/etc/", 0755),
			),
			w: tartest.TarAll(
				tc.Dir("/etc/subdir", 0755),
				tc.Dir("/etc/subdir2/", 0755),
				tc.Dir("/etc/subdir2/more", 0755),
				tc.Dir("/other/noparent-1/1", 0755),
				tc.Dir("/other/noparent-2/2/", 0755),
			),
			validator: directoriesExist(
				"etc/subdir",
				"etc/subdir2",
				"etc/subdir2/more",
				"other/noparent-1/1",
				"other/noparent-2/2",
			),
		},
	}

	for _, at := range tests {
		t.Run(at.name, makeWriterToTarTest(at.w, at.apply, at.validator, at.err))
	}
}

func testApply(t *testing.T, a fstest.Applier) error {
	td := t.TempDir()
	dest := t.TempDir()

	if err := a.Apply(td); err != nil {
		return fmt.Errorf("failed to apply filesystem changes: %w", err)
	}

	tarArgs := []string{"cf", "-", "-C", td}
	names, err := readDirNames(td)
	if err != nil {
		return fmt.Errorf("failed to read directory names: %w", err)
	}
	tarArgs = append(tarArgs, names...)

	cmd := exec.Command(tarCmd, tarArgs...)

	arch, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	if _, err := Apply(context.Background(), dest, arch); err != nil {
		return fmt.Errorf("failed to apply tar stream: %w", err)
	}

	return fstest.CheckDirectoryEqual(td, dest)
}

func testBaseDiff(t *testing.T, a fstest.Applier) error {
	td := t.TempDir()
	dest := t.TempDir()

	if err := a.Apply(td); err != nil {
		return fmt.Errorf("failed to apply filesystem changes: %w", err)
	}

	arch := Diff(context.Background(), "", td)

	cmd := exec.Command(tarCmd, "xf", "-", "-C", dest)
	cmd.Stdin = arch
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		fmt.Println(stderr.String())
		return fmt.Errorf("tar command failed: %w", err)
	}

	return fstest.CheckDirectoryEqual(td, dest)
}

func testDiffApply(t *testing.T, appliers ...fstest.Applier) error {
	td := t.TempDir()
	dest := t.TempDir()

	for _, a := range appliers {
		if err := a.Apply(td); err != nil {
			return fmt.Errorf("failed to apply filesystem changes: %w", err)
		}
	}

	// Apply base changes before diff
	if len(appliers) > 1 {
		for _, a := range appliers[:len(appliers)-1] {
			if err := a.Apply(dest); err != nil {
				return fmt.Errorf("failed to apply base filesystem changes: %w", err)
			}
		}
	}

	diffBytes, err := io.ReadAll(Diff(context.Background(), dest, td))
	if err != nil {
		return fmt.Errorf("failed to create diff: %w", err)
	}

	if _, err := Apply(context.Background(), dest, bytes.NewReader(diffBytes)); err != nil {
		return fmt.Errorf("failed to apply tar stream: %w", err)
	}

	return fstest.CheckDirectoryEqual(td, dest)
}

func makeWriterToTarTest(wt tartest.WriterToTar, a fstest.Applier, validate func(string) error, applyErr error) func(*testing.T) {
	return func(t *testing.T) {
		td := t.TempDir()

		if a != nil {
			if err := a.Apply(td); err != nil {
				t.Fatalf("Failed to apply filesystem to directory: %v", err)
			}
		}

		tr := tartest.TarFromWriterTo(wt)

		if _, err := Apply(context.Background(), td, tr); err != nil {
			if applyErr == nil {
				t.Fatalf("Failed to apply tar: %v", err)
			} else if !errors.Is(err, applyErr) {
				t.Fatalf("Unexpected apply error: %v, expected %v", err, applyErr)
			}
			return
		} else if applyErr != nil {
			t.Fatalf("Expected apply error, got none: %v", applyErr)
		}

		if validate != nil {
			if err := validate(td); err != nil {
				t.Errorf("Validation failed: %v", err)
			}

		}

	}
}

func TestDiffTar(t *testing.T) {
	tests := []struct {
		name       string
		validators []tarEntryValidator
		a          fstest.Applier
		b          fstest.Applier
	}{
		{
			name:       "EmptyDiff",
			validators: []tarEntryValidator{},
			a: fstest.Apply(
				fstest.CreateDir("/etc/", 0755),
			),
			b: fstest.Apply(),
		},
		{
			name: "ParentInclusion",
			validators: []tarEntryValidator{
				dirEntry("d1/", 0755),
				dirEntry("d1/d/", 0700),
				dirEntry("d2/", 0770),
				fileEntry("d2/f", []byte("ok"), 0644),
			},
			a: fstest.Apply(
				fstest.CreateDir("/d1/", 0755),
				fstest.CreateDir("/d2/", 0770),
			),
			b: fstest.Apply(
				fstest.CreateDir("/d1/d", 0700),
				fstest.CreateFile("/d2/f", []byte("ok"), 0644),
			),
		},
		{
			name: "HardlinkParentInclusion",
			validators: []tarEntryValidator{
				dirEntry("d2/", 0755),
				fileEntry("d2/l1", []byte("link me"), 0644),
				// d1/f1 and its parent is included after the new link,
				// before the new link was included, these files would
				// not have been needed
				dirEntry("d1/", 0755),
				linkEntry("d1/f1", "d2/l1"),
				dirEntry("d3/", 0755),
				fileEntry("d3/l1", []byte("link me"), 0644),
				dirEntry("d4/", 0755),
				linkEntry("d4/f1", "d3/l1"),
				dirEntry("d6/", 0755),
				whiteoutEntry("d6/l1"),
				whiteoutEntry("d6/l2"),
			},
			a: fstest.Apply(
				fstest.CreateDir("/d1/", 0755),
				fstest.CreateFile("/d1/f1", []byte("link me"), 0644),
				fstest.CreateDir("/d2/", 0755),
				fstest.CreateFile("/d2/f1", []byte("link me"), 0644),
				fstest.CreateDir("/d3/", 0755),
				fstest.CreateDir("/d4/", 0755),
				fstest.CreateFile("/d4/f1", []byte("link me"), 0644),
				fstest.CreateDir("/d5/", 0755),
				fstest.CreateFile("/d5/f1", []byte("link me"), 0644),
				fstest.CreateDir("/d6/", 0755),
				fstest.Link("/d1/f1", "/d6/l1"),
				fstest.Link("/d5/f1", "/d6/l2"),
			),
			b: fstest.Apply(
				fstest.Link("/d1/f1", "/d2/l1"),
				fstest.Link("/d4/f1", "/d3/l1"),
				fstest.Remove("/d6/l1"),
				fstest.Remove("/d6/l2"),
			),
		},
		{
			name: "UpdateDirectoryPermission",
			validators: []tarEntryValidator{
				dirEntry("d1/", 0777),
				dirEntry("d1/d/", 0700),
				dirEntry("d2/", 0770),
				fileEntry("d2/f", []byte("ok"), 0644),
			},
			a: fstest.Apply(
				fstest.CreateDir("/d1/", 0755),
				fstest.CreateDir("/d2/", 0770),
			),
			b: fstest.Apply(
				fstest.Chmod("/d1", 0777),
				fstest.CreateDir("/d1/d", 0700),
				fstest.CreateFile("/d2/f", []byte("ok"), 0644),
			),
		},
		{
			name: "HardlinkUpdatedParent",
			validators: []tarEntryValidator{
				dirEntry("d1/", 0777),
				dirEntry("d2/", 0755),
				fileEntry("d2/l1", []byte("link me"), 0644),
				// d1/f1 is included after the new link, its
				// parent has already changed and therefore
				// only the linked file is included
				linkEntry("d1/f1", "d2/l1"),
				dirEntry("d4/", 0777),
				fileEntry("d4/l1", []byte("link me"), 0644),
				dirEntry("d3/", 0755),
				linkEntry("d3/f1", "d4/l1"),
			},
			a: fstest.Apply(
				fstest.CreateDir("/d1/", 0755),
				fstest.CreateFile("/d1/f1", []byte("link me"), 0644),
				fstest.CreateDir("/d2/", 0755),
				fstest.CreateFile("/d2/f1", []byte("link me"), 0644),
				fstest.CreateDir("/d3/", 0755),
				fstest.CreateFile("/d3/f1", []byte("link me"), 0644),
				fstest.CreateDir("/d4/", 0755),
			),
			b: fstest.Apply(
				fstest.Chmod("/d1", 0777),
				fstest.Link("/d1/f1", "/d2/l1"),
				fstest.Chmod("/d4", 0777),
				fstest.Link("/d3/f1", "/d4/l1"),
			),
		},
		{
			name: "WhiteoutIncludesParents",
			validators: []tarEntryValidator{
				dirEntry("d1/", 0755),
				whiteoutEntry("d1/f1"),
				dirEntry("d2/", 0755),
				whiteoutEntry("d2/f1"),
				fileEntry("d2/f2", []byte("content"), 0777),
				dirEntry("d3/", 0755),
				whiteoutEntry("d3/f1"),
				fileEntry("d3/f2", []byte("content"), 0644),
				dirEntry("d4/", 0755),
				fileEntry("d4/f0", []byte("content"), 0644),
				whiteoutEntry("d4/f1"),
				whiteoutEntry("d5"),
			},
			a: fstest.Apply(
				fstest.CreateDir("/d1/", 0755),
				fstest.CreateFile("/d1/f1", []byte("content"), 0644),
				fstest.CreateDir("/d2/", 0755),
				fstest.CreateFile("/d2/f1", []byte("content"), 0644),
				fstest.CreateFile("/d2/f2", []byte("content"), 0644),
				fstest.CreateDir("/d3/", 0755),
				fstest.CreateFile("/d3/f1", []byte("content"), 0644),
				fstest.CreateDir("/d4/", 0755),
				fstest.CreateFile("/d4/f1", []byte("content"), 0644),
				fstest.CreateDir("/d5/", 0755),
				fstest.CreateFile("/d5/f1", []byte("content"), 0644),
			),
			b: fstest.Apply(
				fstest.Remove("/d1/f1"),
				fstest.Remove("/d2/f1"),
				fstest.Chmod("/d2/f2", 0777),
				fstest.Remove("/d3/f1"),
				fstest.CreateFile("/d3/f2", []byte("content"), 0644),
				fstest.Remove("/d4/f1"),
				fstest.CreateFile("/d4/f0", []byte("content"), 0644),
				fstest.RemoveAll("/d5"),
			),
		},
		{
			name: "WhiteoutParentRemoval",
			validators: []tarEntryValidator{
				whiteoutEntry("d1"),
				whiteoutEntry("d2"),
				dirEntry("d3/", 0755),
			},
			a: fstest.Apply(
				fstest.CreateDir("/d1/", 0755),
				fstest.CreateDir("/d2/", 0755),
				fstest.CreateFile("/d2/f1", []byte("content"), 0644),
			),
			b: fstest.Apply(
				fstest.RemoveAll("/d1"),
				fstest.RemoveAll("/d2"),
				fstest.CreateDir("/d3/", 0755),
			),
		},
		{
			name: "IgnoreSockets",
			validators: []tarEntryValidator{
				fileEntry("f2", []byte("content"), 0644),
				// There should be _no_ socket here, despite the fstest.CreateSocket below
				fileEntry("f3", []byte("content"), 0644),
			},
			a: fstest.Apply(
				fstest.CreateFile("/f1", []byte("content"), 0644),
			),
			b: fstest.Apply(
				fstest.CreateFile("/f2", []byte("content"), 0644),
				fstest.CreateSocket("/s0", 0644),
				fstest.CreateFile("/f3", []byte("content"), 0644),
			),
		},
	}

	for _, at := range tests {
		t.Run(at.name, makeDiffTarTest(at.validators, at.a, at.b))
	}
}

func TestSourceDateEpoch(t *testing.T) {
	sourceDateEpoch, err := time.Parse(time.RFC3339, "2022-01-23T12:34:56Z")
	require.NoError(t, err)
	past, err := time.Parse(time.RFC3339, "2022-01-01T00:00:00Z")
	require.NoError(t, err)
	require.True(t, past.Before(sourceDateEpoch))
	veryRecent := time.Now()
	require.True(t, veryRecent.After(sourceDateEpoch))

	opts := []WriteDiffOpt{WithSourceDateEpoch(&sourceDateEpoch)}
	validators := []tarEntryValidator{
		composeValidators(whiteoutEntry("f1"), requireModTime(time.Unix(0, 0).UTC())), // not sourceDateEpoch
		composeValidators(fileEntry("f2", []byte("content2"), 0644), requireModTime(past)),
		composeValidators(fileEntry("f3", []byte("content3"), 0644), requireModTime(sourceDateEpoch)),
	}
	a := fstest.Apply(
		fstest.CreateFile("/f1", []byte("content"), 0644),
	)
	b := fstest.Apply(
		// Remove f1; the timestamp of the tar entry will be sourceDateEpoch
		fstest.RemoveAll("/f1"),
		// Create f2 with the past timestamp; the timestamp of the tar entry will be past (< sourceDateEpoch)
		fstest.CreateFile("/f2", []byte("content2"), 0644),
		fstest.Chtimes("/f2", past, past),
		// Create f3 with the veryRecent timestamp; the timestamp of the tar entry will be sourceDateEpoch
		fstest.CreateFile("/f3", []byte("content3"), 0644),
		fstest.Chtimes("/f3", veryRecent, veryRecent),
	)
	makeDiffTarTest(validators, a, b, opts...)(t)
	if testing.Short() {
		t.Skip("short: skipping repro test")
	}
	makeDiffTarReproTest(a, b, opts...)(t)
}

type tarEntryValidator func(*tar.Header, []byte) error

func composeValidators(vv ...tarEntryValidator) tarEntryValidator {
	return func(hdr *tar.Header, b []byte) error {
		for _, v := range vv {
			if err := v(hdr, b); err != nil {
				return err
			}
		}
		return nil
	}
}

func dirEntry(name string, mode int) tarEntryValidator {
	return func(hdr *tar.Header, b []byte) error {
		if hdr.Typeflag != tar.TypeDir {
			return errors.New("not directory type")
		}
		if hdr.Name != name {
			return fmt.Errorf("wrong name %q, expected %q", hdr.Name, name)
		}
		if hdr.Mode != int64(mode) {
			return fmt.Errorf("wrong mode %o, expected %o", hdr.Mode, mode)
		}
		return nil
	}
}

func fileEntry(name string, expected []byte, mode int) tarEntryValidator {
	return func(hdr *tar.Header, b []byte) error {
		if hdr.Typeflag != tar.TypeReg {
			return errors.New("not file type")
		}
		if hdr.Name != name {
			return fmt.Errorf("wrong name %q, expected %q", hdr.Name, name)
		}
		if hdr.Mode != int64(mode) {
			return fmt.Errorf("wrong mode %o, expected %o", hdr.Mode, mode)
		}
		if !bytes.Equal(b, expected) {
			return errors.New("different file content")
		}
		return nil
	}
}

func linkEntry(name, link string) tarEntryValidator {
	return func(hdr *tar.Header, b []byte) error {
		if hdr.Typeflag != tar.TypeLink {
			return errors.New("not link type")
		}
		if hdr.Name != name {
			return fmt.Errorf("wrong name %q, expected %q", hdr.Name, name)
		}
		if hdr.Linkname != link {
			return fmt.Errorf("wrong link %q, expected %q", hdr.Linkname, link)
		}
		return nil
	}
}

func whiteoutEntry(name string) tarEntryValidator {
	whiteOutDir := filepath.Dir(name)
	whiteOutBase := filepath.Base(name)
	whiteOut := filepath.Join(whiteOutDir, whiteoutPrefix+whiteOutBase)

	return func(hdr *tar.Header, b []byte) error {
		if hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("not file type: %q", hdr.Typeflag)
		}
		if hdr.Name != whiteOut {
			return fmt.Errorf("wrong name %q, expected whiteout %q", hdr.Name, name)
		}
		return nil
	}
}

func requireModTime(expected time.Time) tarEntryValidator {
	return func(hdr *tar.Header, b []byte) error {
		if !hdr.ModTime.Equal(expected) {
			return fmt.Errorf("expected ModTime %v, got %v", expected, hdr.ModTime)
		}
		return nil
	}
}

func makeDiffTarTest(validators []tarEntryValidator, a, b fstest.Applier, opts ...WriteDiffOpt) func(*testing.T) {
	return func(t *testing.T) {
		ad := t.TempDir()
		if err := a.Apply(ad); err != nil {
			t.Fatalf("failed to apply a: %v", err)
		}

		bd := t.TempDir()
		if err := fs.CopyDir(bd, ad); err != nil {
			t.Fatalf("failed to copy dir: %v", err)
		}
		if err := b.Apply(bd); err != nil {
			t.Fatalf("failed to apply b: %v", err)
		}

		rc := Diff(context.Background(), ad, bd, opts...)
		defer rc.Close()

		tr := tar.NewReader(rc)
		for i := 0; ; i++ {
			hdr, err := tr.Next()
			if err != nil {
				if err == io.EOF {
					break
				}
				t.Fatalf("tar read error: %v", err)
			}
			var b []byte
			if hdr.Typeflag == tar.TypeReg && hdr.Size > 0 {
				b, err = io.ReadAll(tr)
				if err != nil {
					t.Fatalf("tar read file error: %v", err)
				}
			}
			if i >= len(validators) {
				t.Fatal("no validator for entry")
			}
			if err := validators[i](hdr, b); err != nil {
				t.Fatalf("tar entry[%d] validation fail: %#v", i, err)
			}
		}
	}
}

func makeDiffTar(t *testing.T, a, b fstest.Applier, opts ...WriteDiffOpt) (digest.Digest, []byte) {
	ad := t.TempDir()
	if err := a.Apply(ad); err != nil {
		t.Fatalf("failed to apply a: %v", err)
	}

	bd := t.TempDir()
	if err := fs.CopyDir(bd, ad); err != nil {
		t.Fatalf("failed to copy dir: %v", err)
	}
	if err := b.Apply(bd); err != nil {
		t.Fatalf("failed to apply b: %v", err)
	}

	rc := Diff(context.Background(), ad, bd, opts...)
	defer rc.Close()
	var buf bytes.Buffer
	r := io.TeeReader(rc, &buf)
	dgst, err := digest.FromReader(r)
	if err != nil {
		t.Fatal(err)
	}
	if err = rc.Close(); err != nil {
		t.Fatal(err)
	}
	return dgst, buf.Bytes()
}

func makeDiffTarReproTest(a, b fstest.Applier, opts ...WriteDiffOpt) func(*testing.T) {
	return func(t *testing.T) {
		const (
			count = 30
			delay = 100 * time.Millisecond
		)
		var lastDigest digest.Digest
		for i := 0; i < count; i++ {
			dgst, _ := makeDiffTar(t, a, b, opts...)
			t.Logf("#%02d: %v: digest %s", i, time.Now(), dgst)
			if lastDigest == "" {
				lastDigest = dgst
			} else if dgst != lastDigest {
				t.Fatalf("expected digest %s, got %s", lastDigest, dgst)
			}
			time.Sleep(delay)
		}
	}
}

type diffApplier struct{}

func (d diffApplier) TestContext(ctx context.Context) (context.Context, func(), error) {
	base, err := os.MkdirTemp("", "test-diff-apply-")
	if err != nil {
		return ctx, nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	return context.WithValue(ctx, d, base), func() {
		os.RemoveAll(base)
	}, nil
}

func (d diffApplier) Apply(ctx context.Context, a fstest.Applier) (string, func(), error) {
	base := ctx.Value(d).(string)

	applyCopy, err := os.MkdirTemp("", "test-diffapply-apply-copy-")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(applyCopy)
	if err = fs.CopyDir(applyCopy, base); err != nil {
		return "", nil, fmt.Errorf("failed to copy base: %w", err)
	}
	if err := a.Apply(applyCopy); err != nil {
		return "", nil, fmt.Errorf("failed to apply changes to copy of base: %w", err)
	}

	diffBytes, err := io.ReadAll(Diff(ctx, base, applyCopy))
	if err != nil {
		return "", nil, fmt.Errorf("failed to create diff: %w", err)
	}

	if _, err = Apply(ctx, base, bytes.NewReader(diffBytes)); err != nil {
		return "", nil, fmt.Errorf("failed to apply tar stream: %w", err)
	}

	return base, nil, nil
}

func readDirNames(p string) ([]string, error) {
	fis, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(fis))
	for i, fi := range fis {
		names[i] = fi.Name()
	}
	return names, nil
}

func requireTar(t *testing.T) {
	if _, err := exec.LookPath(tarCmd); err != nil {
		t.Skipf("%s not found, skipping", tarCmd)
	}
}
