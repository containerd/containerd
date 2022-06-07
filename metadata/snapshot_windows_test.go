//go:build windows
// +build windows

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

package metadata

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/windows"
	bolt "go.etcd.io/bbolt"
)

func initTestDB(t *testing.T, root string, sns map[string]snapshots.Snapshotter) (*DB, func()) {
	cs, err := local.NewStore(filepath.Join(root, "content"))
	if err != nil {
		t.Fatal(err)
	}

	bdb, err := bolt.Open(filepath.Join(root, "metadata.db"), 0644, nil)
	if err != nil {
		t.Fatal(err)
	}

	db := NewDB(bdb, cs, sns, WithSnapshotPolicyShared)
	if err := db.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db, func() { db.db.Close() }
}

func TestSnapshotSharingSimple(t *testing.T) {
	root, err := ioutil.TempDir("", t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)

	windowsSn, err := windows.NewSnapshotter(filepath.Join(root, "snapshotter"))
	if err != nil {
		t.Fatal(err)
	}

	sns := map[string]snapshots.Snapshotter{"windows": windowsSn}

	db, closer := initTestDB(t, root, sns)
	defer closer()
	defer windowsSn.Close()

	testsn := db.Snapshotter("windows")

	ctxns1 := namespaces.WithNamespace(context.Background(), "testns1")
	ctxns2 := namespaces.WithNamespace(context.Background(), "testns2")

	labelOpt := snapshots.WithLabels(map[string]string{labelSnapshotRef: "ref1"})

	if _, err = testsn.Prepare(ctxns1, "extract sn1", "", labelOpt); err != nil {
		t.Fatal(err)
	}

	if err = testsn.Commit(ctxns1, "ref1", "extract sn1", labelOpt); err != nil {
		t.Fatal(err)
	}

	_, err = testsn.Prepare(ctxns2, "extract sn2", "", labelOpt)
	if err == nil || !snapshots.IsTargetSnapshotExists(err) {
		t.Fatalf("expected already exists error, got: %s", err)
	}
}

func TestSnapshotSharingAfterRemove(t *testing.T) {
	root, err := ioutil.TempDir("", t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)

	windowsSn, err := windows.NewSnapshotter(filepath.Join(root, "snapshotter"))
	if err != nil {
		t.Fatal(err)
	}

	sns := map[string]snapshots.Snapshotter{"windows": windowsSn}

	db, closer := initTestDB(t, root, sns)
	defer closer()
	defer windowsSn.Close()

	testsn := db.Snapshotter("windows")

	ctxns1 := namespaces.WithNamespace(context.Background(), "testns1")
	ctxns2 := namespaces.WithNamespace(context.Background(), "testns2")

	labelOpt := snapshots.WithLabels(map[string]string{labelSnapshotRef: "ref1"})

	if _, err = testsn.Prepare(ctxns1, "extract sn1", "", labelOpt); err != nil {
		t.Fatal(err)
	}

	if err = testsn.Commit(ctxns1, "ref1", "extract sn1", labelOpt); err != nil {
		t.Fatal(err)
	}

	_, err = testsn.Prepare(ctxns2, "extract sn2", "", labelOpt)
	if err == nil || !snapshots.IsTargetSnapshotExists(err) {
		t.Fatalf("expected already exists error, got: %s", err)
	}

	if err = testsn.Remove(ctxns1, "ref1"); err != nil {
		t.Fatal(err)
	}

	// check if we can still access from other namespace
	if si, err := testsn.Stat(ctxns2, "ref1"); err != nil {
		t.Fatal(err)
	} else if si.Kind != snapshots.KindCommitted || si.Labels[labelSnapshotRef] != "ref1" {
		t.Fatalf("expected snapshot Info doesn't match: %+v\n", si)
	}

	if err = testsn.Remove(ctxns2, "ref1"); err != nil {
		t.Fatal(err)
	}
}
