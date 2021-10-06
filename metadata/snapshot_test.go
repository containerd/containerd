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
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/filters"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/testutil"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/native"
	"github.com/containerd/containerd/snapshots/testsuite"

	bolt "go.etcd.io/bbolt"
)

func newTestSnapshotter(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
	nativeRoot := filepath.Join(root, "native")
	if err := os.Mkdir(nativeRoot, 0770); err != nil {
		return nil, nil, err
	}
	snapshotter, err := native.NewSnapshotter(nativeRoot)
	if err != nil {
		return nil, nil, err
	}

	db, err := bolt.Open(filepath.Join(root, "metadata.db"), 0660, nil)
	if err != nil {
		return nil, nil, err
	}

	sn := NewDB(db, nil, map[string]snapshots.Snapshotter{"native": snapshotter}).Snapshotter("native")

	return sn, func() error {
		if err := sn.Close(); err != nil {
			return err
		}
		return db.Close()
	}, nil
}

func TestMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("snapshotter not implemented on windows")
	}
	// Snapshot tests require mounting, still requires root
	testutil.RequiresRoot(t)
	testsuite.SnapshotterSuite(t, "Metadata", newTestSnapshotter)
}

func TestSnapshotterWithRef(t *testing.T) {
	ctx, db, done := testDB(t, withSnapshotter("tmp", func(string) (snapshots.Snapshotter, error) {
		return NewTmpSnapshotter(), nil
	}))
	defer done()

	sn := db.Snapshotter("tmp")

	test1opt := snapshots.WithLabels(
		map[string]string{
			labelSnapshotRef: "test1",
		},
	)

	_, err := sn.Prepare(ctx, "test1-tmp", "", test1opt)
	if err != nil {
		t.Fatal(err)
	}

	err = sn.Commit(ctx, "test1", "test1-tmp", test1opt)
	if err != nil {
		t.Fatal(err)
	}

	ctx2 := namespaces.WithNamespace(ctx, "testing2")

	_, err = sn.Prepare(ctx2, "test1-tmp", "", test1opt)
	if err == nil {
		t.Fatal("expected already exists error")
	} else if !errdefs.IsAlreadyExists(err) {
		t.Fatal(err)
	}

	// test1 should now be in the namespace
	_, err = sn.Stat(ctx2, "test1")
	if err != nil {
		t.Fatal(err)
	}

	test2opt := snapshots.WithLabels(
		map[string]string{
			labelSnapshotRef: "test2",
		},
	)

	_, err = sn.Prepare(ctx2, "test2-tmp", "test1", test2opt)
	if err != nil {
		t.Fatal(err)
	}

	// In original namespace, but not committed
	_, err = sn.Prepare(ctx, "test2-tmp", "test1", test2opt)
	if err != nil {
		t.Fatal(err)
	}

	err = sn.Commit(ctx2, "test2", "test2-tmp", test2opt)
	if err != nil {
		t.Fatal(err)
	}

	// See note in Commit function for why
	// this does not return ErrAlreadyExists
	err = sn.Commit(ctx, "test2", "test2-tmp", test2opt)
	if err != nil {
		t.Fatal(err)
	}

	// This should error out, already exists in namespace
	// despite mismatched parent
	_, err = sn.Prepare(ctx2, "test2-tmp-again", "", test2opt)
	if err == nil {
		t.Fatal("expected already exists error")
	} else if !errdefs.IsAlreadyExists(err) {
		t.Fatal(err)
	}

	// In original namespace, but already exists
	_, err = sn.Prepare(ctx, "test2-tmp-again", "test1", test2opt)
	if err == nil {
		t.Fatal("expected already exists error")
	} else if !errdefs.IsAlreadyExists(err) {
		t.Fatal(err)
	}

	// Now try a third namespace

	ctx3 := namespaces.WithNamespace(ctx, "testing3")

	// This should error out, matching parent not found
	_, err = sn.Prepare(ctx3, "test2-tmp", "", test2opt)
	if err != nil {
		t.Fatal(err)
	}

	// Remove, not going to use yet
	err = sn.Remove(ctx3, "test2-tmp")
	if err != nil {
		t.Fatal(err)
	}

	_, err = sn.Prepare(ctx3, "test2-tmp", "test1", test2opt)
	if err == nil {
		t.Fatal("expected not error")
	} else if !errdefs.IsNotFound(err) {
		t.Fatal(err)
	}

	_, err = sn.Prepare(ctx3, "test1-tmp", "", test1opt)
	if err == nil {
		t.Fatal("expected already exists error")
	} else if !errdefs.IsAlreadyExists(err) {
		t.Fatal(err)
	}

	_, err = sn.Prepare(ctx3, "test2-tmp", "test1", test2opt)
	if err == nil {
		t.Fatal("expected already exists error")
	} else if !errdefs.IsAlreadyExists(err) {
		t.Fatal(err)
	}
}

func TestFilterInheritedLabels(t *testing.T) {
	tests := []struct {
		labels   map[string]string
		expected map[string]string
	}{
		{
			nil,
			nil,
		},
		{
			map[string]string{},
			map[string]string{},
		},
		{
			map[string]string{"": ""},
			map[string]string{},
		},
		{
			map[string]string{"foo": "bar"},
			map[string]string{},
		},
		{
			map[string]string{inheritedLabelsPrefix + "foo": "bar"},
			map[string]string{inheritedLabelsPrefix + "foo": "bar"},
		},
		{
			map[string]string{inheritedLabelsPrefix + "foo": "bar", "qux": "qaz"},
			map[string]string{inheritedLabelsPrefix + "foo": "bar"},
		},
	}

	for _, test := range tests {
		if actual := snapshots.FilterInheritedLabels(test.labels); !reflect.DeepEqual(actual, test.expected) {
			t.Fatalf("expected %v but got %v", test.expected, actual)
		}
	}
}

type tmpSnapshotter struct {
	l         sync.Mutex
	snapshots map[string]snapshots.Info
	targets   map[string][]string
}

func NewTmpSnapshotter() snapshots.Snapshotter {
	return &tmpSnapshotter{
		snapshots: map[string]snapshots.Info{},
		targets:   map[string][]string{},
	}
}

func (s *tmpSnapshotter) Stat(ctx context.Context, key string) (snapshots.Info, error) {
	s.l.Lock()
	defer s.l.Unlock()
	i, ok := s.snapshots[key]
	if !ok {
		return snapshots.Info{}, errdefs.ErrNotFound
	}
	return i, nil
}

func (s *tmpSnapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
	s.l.Lock()
	defer s.l.Unlock()

	i, ok := s.snapshots[info.Name]
	if !ok {
		return snapshots.Info{}, errdefs.ErrNotFound
	}

	for k, v := range info.Labels {
		i.Labels[k] = v
	}

	s.snapshots[i.Name] = i

	return i, nil
}

func (s *tmpSnapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	s.l.Lock()
	defer s.l.Unlock()
	_, ok := s.snapshots[key]
	if !ok {
		return snapshots.Usage{}, errdefs.ErrNotFound
	}
	return snapshots.Usage{}, nil
}

func (s *tmpSnapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	s.l.Lock()
	defer s.l.Unlock()
	_, ok := s.snapshots[key]
	if !ok {
		return nil, errdefs.ErrNotFound
	}
	return []mount.Mount{}, nil
}

func (s *tmpSnapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return s.create(ctx, key, parent, snapshots.KindActive, opts...)
}

func (s *tmpSnapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return s.create(ctx, key, parent, snapshots.KindView, opts...)
}

func (s *tmpSnapshotter) create(ctx context.Context, key, parent string, kind snapshots.Kind, opts ...snapshots.Opt) ([]mount.Mount, error) {
	s.l.Lock()
	defer s.l.Unlock()

	var base snapshots.Info
	for _, opt := range opts {
		if err := opt(&base); err != nil {
			return nil, err
		}
	}
	base.Name = key
	base.Kind = kind

	target := base.Labels[labelSnapshotRef]
	if target != "" {
		for _, name := range s.targets[target] {
			if s.snapshots[name].Parent == parent {
				return nil, fmt.Errorf("found target: %w", errdefs.ErrAlreadyExists)
			}
		}
	}

	if parent != "" {
		_, ok := s.snapshots[parent]
		if !ok {
			return nil, errdefs.ErrNotFound
		}
		base.Parent = parent
	}

	ts := time.Now().UTC()
	base.Created = ts
	base.Updated = ts

	s.snapshots[base.Name] = base

	return []mount.Mount{}, nil
}

func (s *tmpSnapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	s.l.Lock()
	defer s.l.Unlock()

	var base snapshots.Info
	for _, opt := range opts {
		if err := opt(&base); err != nil {
			return err
		}
	}
	base.Name = name
	base.Kind = snapshots.KindCommitted

	if _, ok := s.snapshots[name]; ok {
		return fmt.Errorf("found name: %w", errdefs.ErrAlreadyExists)
	}

	src, ok := s.snapshots[key]
	if !ok {
		return errdefs.ErrNotFound
	}
	if src.Kind == snapshots.KindCommitted {
		return errdefs.ErrInvalidArgument
	}
	base.Parent = src.Parent

	ts := time.Now().UTC()
	base.Created = ts
	base.Updated = ts

	s.snapshots[name] = base
	delete(s.snapshots, key)

	if target := base.Labels[labelSnapshotRef]; target != "" {
		s.targets[target] = append(s.targets[target], name)
	}

	return nil
}

func (s *tmpSnapshotter) Remove(ctx context.Context, key string) error {
	s.l.Lock()
	defer s.l.Unlock()

	sn, ok := s.snapshots[key]
	if !ok {
		return errdefs.ErrNotFound
	}
	delete(s.snapshots, key)

	// scan and remove all instances of name as a target
	for ref, names := range s.targets {
		for i := range names {
			if names[i] == sn.Name {
				if len(names) == 1 {
					delete(s.targets, ref)
				} else {
					copy(names[i:], names[i+1:])
					s.targets[ref] = names[:len(names)-1]
				}
				break
			}
		}
	}

	return nil
}

func (s *tmpSnapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, fs ...string) error {
	s.l.Lock()
	defer s.l.Unlock()

	filter, err := filters.ParseAll(fs...)
	if err != nil {
		return err
	}

	// call func for each
	for _, i := range s.snapshots {
		if filter.Match(adaptSnapshot(i)) {
			if err := fn(ctx, i); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *tmpSnapshotter) Close() error {
	return nil
}
