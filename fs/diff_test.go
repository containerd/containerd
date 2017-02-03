package fs

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/containerd/fs/fstest"
	"github.com/pkg/errors"
)

// TODO: Additional tests
// - capability test (requires privilege)
// - chown test (requires privilege)
// - symlink test
// - hardlink test

func TestSimpleDiff(t *testing.T) {
	l1 := fstest.MultiApply(
		fstest.CreateDirectory("/etc", 0755),
		fstest.NewTestFile("/etc/hosts", []byte("mydomain 10.0.0.1"), 0644),
		fstest.NewTestFile("/etc/profile", []byte("PATH=/usr/bin"), 0644),
		fstest.NewTestFile("/etc/unchanged", []byte("PATH=/usr/bin"), 0644),
		fstest.NewTestFile("/etc/unexpected", []byte("#!/bin/sh"), 0644),
	)
	l2 := fstest.MultiApply(
		fstest.NewTestFile("/etc/hosts", []byte("mydomain 10.0.0.120"), 0644),
		fstest.NewTestFile("/etc/profile", []byte("PATH=/usr/bin"), 0666),
		fstest.CreateDirectory("/root", 0700),
		fstest.NewTestFile("/root/.bashrc", []byte("PATH=/usr/sbin:/usr/bin"), 0644),
		fstest.RemoveFile("/etc/unexpected"),
	)
	diff := []Change{
		Modify("/etc/hosts"),
		Modify("/etc/profile"),
		Delete("/etc/unexpected"),
		Add("/root"),
		Add("/root/.bashrc"),
	}

	if err := testDiffWithBase(l1, l2, diff); err != nil {
		t.Fatalf("Failed diff with base: %+v", err)
	}
}

func TestDirectoryReplace(t *testing.T) {
	l1 := fstest.MultiApply(
		fstest.CreateDirectory("/dir1", 0755),
		fstest.NewTestFile("/dir1/f1", []byte("#####"), 0644),
		fstest.CreateDirectory("/dir1/f2", 0755),
		fstest.NewTestFile("/dir1/f2/f3", []byte("#!/bin/sh"), 0644),
	)
	l2 := fstest.MultiApply(
		fstest.NewTestFile("/dir1/f11", []byte("#New file here"), 0644),
		fstest.RemoveFile("/dir1/f2"),
		fstest.NewTestFile("/dir1/f2", []byte("Now file"), 0666),
	)
	diff := []Change{
		Add("/dir1/f11"),
		Modify("/dir1/f2"),
	}

	if err := testDiffWithBase(l1, l2, diff); err != nil {
		t.Fatalf("Failed diff with base: %+v", err)
	}
}

func TestRemoveDirectoryTree(t *testing.T) {
	l1 := fstest.MultiApply(
		fstest.CreateDirectory("/dir1/dir2/dir3", 0755),
		fstest.NewTestFile("/dir1/f1", []byte("f1"), 0644),
		fstest.NewTestFile("/dir1/dir2/f2", []byte("f2"), 0644),
	)
	l2 := fstest.MultiApply(
		fstest.RemoveFile("/dir1"),
	)
	diff := []Change{
		Delete("/dir1"),
	}

	if err := testDiffWithBase(l1, l2, diff); err != nil {
		t.Fatalf("Failed diff with base: %+v", err)
	}
}

func TestFileReplace(t *testing.T) {
	l1 := fstest.MultiApply(
		fstest.NewTestFile("/dir1", []byte("a file, not a directory"), 0644),
	)
	l2 := fstest.MultiApply(
		fstest.RemoveFile("/dir1"),
		fstest.CreateDirectory("/dir1/dir2", 0755),
		fstest.NewTestFile("/dir1/dir2/f1", []byte("also a file"), 0644),
	)
	diff := []Change{
		Modify("/dir1"),
		Add("/dir1/dir2"),
		Add("/dir1/dir2/f1"),
	}

	if err := testDiffWithBase(l1, l2, diff); err != nil {
		t.Fatalf("Failed diff with base: %+v", err)
	}
}

func TestUpdateWithSameTime(t *testing.T) {
	tt := time.Now().Truncate(time.Second)
	t1 := tt.Add(5 * time.Nanosecond)
	t2 := tt.Add(6 * time.Nanosecond)
	l1 := fstest.MultiApply(
		fstest.NewTestFile("/file-modified-time", []byte("1"), 0644),
		fstest.Chtime("/file-modified-time", t1),
		fstest.NewTestFile("/file-no-change", []byte("1"), 0644),
		fstest.Chtime("/file-no-change", t1),
		fstest.NewTestFile("/file-same-time", []byte("1"), 0644),
		fstest.Chtime("/file-same-time", t1),
		fstest.NewTestFile("/file-truncated-time-1", []byte("1"), 0644),
		fstest.Chtime("/file-truncated-time-1", t1),
		fstest.NewTestFile("/file-truncated-time-2", []byte("1"), 0644),
		fstest.Chtime("/file-truncated-time-2", tt),
	)
	l2 := fstest.MultiApply(
		fstest.NewTestFile("/file-modified-time", []byte("2"), 0644),
		fstest.Chtime("/file-modified-time", t2),
		fstest.NewTestFile("/file-no-change", []byte("1"), 0644),
		fstest.Chtime("/file-no-change", tt), // use truncated time, should be regarded as no change
		fstest.NewTestFile("/file-same-time", []byte("2"), 0644),
		fstest.Chtime("/file-same-time", t1),
		fstest.NewTestFile("/file-truncated-time-1", []byte("2"), 0644),
		fstest.Chtime("/file-truncated-time-1", tt),
		fstest.NewTestFile("/file-truncated-time-2", []byte("2"), 0644),
		fstest.Chtime("/file-truncated-time-2", tt),
	)
	diff := []Change{
		// "/file-same-time" excluded because matching non-zero nanosecond values
		Modify("/file-modified-time"),
		Modify("/file-truncated-time-1"),
		Modify("/file-truncated-time-2"),
	}

	if err := testDiffWithBase(l1, l2, diff); err != nil {
		t.Fatalf("Failed diff with base: %+v", err)
	}
}

func testDiffWithBase(base, diff fstest.Applier, expected []Change) error {
	t1, err := ioutil.TempDir("", "diff-with-base-lower-")
	if err != nil {
		return errors.Wrap(err, "failed to create temp dir")
	}
	defer os.RemoveAll(t1)
	t2, err := ioutil.TempDir("", "diff-with-base-upper-")
	if err != nil {
		return errors.Wrap(err, "failed to create temp dir")
	}
	defer os.RemoveAll(t2)

	if err := base(t1); err != nil {
		return errors.Wrap(err, "failed to apply base filesytem")
	}

	if err := CopyDirectory(t2, t1); err != nil {
		return errors.Wrap(err, "failed to copy base directory")
	}

	if err := diff(t2); err != nil {
		return errors.Wrap(err, "failed to apply diff filesystem")
	}

	changes, err := collectChanges(t2, t1)
	if err != nil {
		return errors.Wrap(err, "failed to collect changes")
	}

	return checkChanges(t2, changes, expected)
}

func TestBaseDirectoryChanges(t *testing.T) {
	apply := fstest.MultiApply(
		fstest.CreateDirectory("/etc", 0755),
		fstest.NewTestFile("/etc/hosts", []byte("mydomain 10.0.0.1"), 0644),
		fstest.NewTestFile("/etc/profile", []byte("PATH=/usr/bin"), 0644),
		fstest.CreateDirectory("/root", 0700),
		fstest.NewTestFile("/root/.bashrc", []byte("PATH=/usr/sbin:/usr/bin"), 0644),
	)
	changes := []Change{
		Add("/etc"),
		Add("/etc/hosts"),
		Add("/etc/profile"),
		Add("/root"),
		Add("/root/.bashrc"),
	}

	if err := testDiffWithoutBase(apply, changes); err != nil {
		t.Fatalf("Failed diff without base: %+v", err)
	}
}

func testDiffWithoutBase(apply fstest.Applier, expected []Change) error {
	tmp, err := ioutil.TempDir("", "diff-without-base-")
	if err != nil {
		return errors.Wrap(err, "failed to create temp dir")
	}
	defer os.RemoveAll(tmp)

	if err := apply(tmp); err != nil {
		return errors.Wrap(err, "failed to apply filesytem changes")
	}

	changes, err := collectChanges(tmp, "")
	if err != nil {
		return errors.Wrap(err, "failed to collect changes")
	}

	return checkChanges(tmp, changes, expected)
}

func checkChanges(root string, changes []testChange, expected []Change) error {
	if len(changes) != len(expected) {
		return errors.Errorf("Unexpected number of changes:\n%s", diffString(convertTestChanges(changes), expected))
	}
	for i := range changes {
		if changes[i].Path != expected[i].Path || changes[i].Kind != expected[i].Kind {
			return errors.Errorf("Unexpected change at %d:\n%s", i, diffString(convertTestChanges(changes), expected))
		}
		if changes[i].Kind != ChangeKindDelete {
			filename := filepath.Join(root, changes[i].Path)
			efi, err := os.Stat(filename)
			if err != nil {
				return errors.Wrapf(err, "failed to stat %q", filename)
			}
			afi := changes[i].FileInfo
			if afi.Size() != efi.Size() {
				return errors.Errorf("Unexpected change size %d, %q has size %d", afi.Size(), filename, efi.Size())
			}
			if afi.Mode() != efi.Mode() {
				return errors.Errorf("Unexpected change mode %s, %q has mode %s", afi.Mode(), filename, efi.Mode())
			}
			if afi.ModTime() != efi.ModTime() {
				return errors.Errorf("Unexpected change modtime %s, %q has modtime %s", afi.ModTime(), filename, efi.ModTime())
			}
			if expected := filepath.Join(root, changes[i].Path); changes[i].Source != expected {
				return errors.Errorf("Unexpected source path %s, expected %s", changes[i].Source, expected)
			}
		}
	}

	return nil
}

type testChange struct {
	Change
	FileInfo os.FileInfo
	Source   string
}

func collectChanges(upper, lower string) ([]testChange, error) {
	changes := []testChange{}
	err := Changes(context.Background(), upper, lower, func(c Change, f os.FileInfo) error {
		changes = append(changes, testChange{
			Change:   c,
			FileInfo: f,
			Source:   filepath.Join(upper, c.Path),
		})
		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to compute changes")
	}

	return changes, nil
}

func convertTestChanges(c []testChange) []Change {
	nc := make([]Change, len(c))
	for i := range c {
		nc[i] = c[i].Change
	}
	return nc
}

func diffString(c1, c2 []Change) string {
	return fmt.Sprintf("got(%d):\n%s\nexpected(%d):\n%s", len(c1), changesString(c1), len(c2), changesString(c2))

}

func changesString(c []Change) string {
	strs := make([]string, len(c))
	for i := range c {
		strs[i] = fmt.Sprintf("\t%s\t%s", c[i].Kind, c[i].Path)
	}
	return strings.Join(strs, "\n")
}

func Add(p string) Change {
	return Change{
		Kind: ChangeKindAdd,
		Path: p,
	}
}

func Delete(p string) Change {
	return Change{
		Kind: ChangeKindDelete,
		Path: p,
	}
}

func Modify(p string) Change {
	return Change{
		Kind: ChangeKindModify,
		Path: p,
	}
}
