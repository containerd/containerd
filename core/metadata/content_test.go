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
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/content/testsuite"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	bolt "go.etcd.io/bbolt"
)

func createContentStore(ctx context.Context, root string, opts ...DBOpt) (context.Context, content.Store, func() error, error) {
	// TODO: Use mocked or in-memory store
	cs, err := local.NewStore(root)
	if err != nil {
		return nil, nil, nil, err
	}

	db, err := bolt.Open(filepath.Join(root, "metadata.db"), 0660, nil)
	if err != nil {
		return nil, nil, nil, err
	}

	var (
		count atomic.Uint64
		name  = testsuite.Name(ctx)
	)
	wrap := func(ctx context.Context, sharedNS bool) (context.Context, func(context.Context) error, error) {
		n := count.Add(1)
		ctx2 := namespaces.WithNamespace(ctx, fmt.Sprintf("%s-n%d", name, n))
		if sharedNS {
			db.Update(func(tx *bolt.Tx) error {
				if ns, err := namespaces.NamespaceRequired(ctx2); err == nil {
					return NewNamespaceStore(tx).SetLabel(ctx2, ns, labels.LabelSharedNamespace, "true")
				}
				return err
			})
		}
		return ctx2, func(context.Context) error {
			return nil
		}, nil
	}
	ctx = testsuite.SetContextWrapper(ctx, wrap)

	return ctx, NewDB(db, cs, nil, opts...).ContentStore(), func() error {
		return db.Close()
	}, nil
}

func createContentStoreWithPolicy(opts ...DBOpt) testsuite.StoreInitFn {
	return func(ctx context.Context, root string) (context.Context, content.Store, func() error, error) {
		return createContentStore(ctx, root, opts...)
	}
}

func TestContent(t *testing.T) {
	testsuite.ContentSuite(t, "metadata", createContentStoreWithPolicy())
	testsuite.ContentCrossNSSharedSuite(t, "metadata", createContentStoreWithPolicy())
	testsuite.ContentCrossNSIsolatedSuite(
		t, "metadata", createContentStoreWithPolicy([]DBOpt{
			WithPolicyIsolated,
		}...))
	testsuite.ContentSharedNSIsolatedSuite(
		t, "metadata", createContentStoreWithPolicy([]DBOpt{
			WithPolicyIsolated,
		}...))
}

func TestContentLeased(t *testing.T) {
	ctx, db := testDB(t)

	cs := db.ContentStore()

	blob := []byte("any content")
	expected := digest.FromBytes(blob)

	lctx, _, err := createLease(ctx, db, "lease-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := content.WriteBlob(lctx, cs, "test-1", bytes.NewReader(blob),
		ocispec.Descriptor{Size: int64(len(blob)), Digest: expected}); err != nil {
		t.Fatal(err)
	}
	if err := checkContentLeased(lctx, db, expected); err != nil {
		t.Fatal("lease checked failed:", err)
	}
	if err := checkIngestLeased(lctx, db, "test-1"); err == nil {
		t.Fatal("test-1 should not be leased after write")
	} else if !errdefs.IsNotFound(err) {
		t.Fatal("lease checked failed:", err)
	}

	lctx, _, err = createLease(ctx, db, "lease-2")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := cs.Writer(lctx,
		content.WithRef("test-2"),
		content.WithDescriptor(ocispec.Descriptor{Size: int64(len(blob)), Digest: expected})); err == nil {
		t.Fatal("expected already exist error")
	} else if !errdefs.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	if err := checkContentLeased(lctx, db, expected); err != nil {
		t.Fatal("lease checked failed:", err)
	}
	if err := checkIngestLeased(lctx, db, "test-2"); err == nil {
		t.Fatal("test-2 should not be leased")
	} else if !errdefs.IsNotFound(err) {
		t.Fatal("lease checked failed:", err)
	}
}

func TestIngestLeased(t *testing.T) {
	ctx, db := testDB(t)
	cs := db.ContentStore()
	blob := []byte("any content")
	expected := digest.FromBytes(blob)

	lctx, _, err := createLease(ctx, db, "lease-1")
	if err != nil {
		t.Fatal(err)
	}

	w, err := cs.Writer(lctx,
		content.WithRef("test-1"),
		content.WithDescriptor(ocispec.Descriptor{Size: int64(len(blob)), Digest: expected}))
	if err != nil {
		t.Fatal(err)
	}
	err = checkIngestLeased(lctx, db, "test-1")
	w.Close()
	if err != nil {
		t.Fatal("lease checked failed:", err)
	}

	if err := cs.Abort(lctx, "test-1"); err != nil {
		t.Fatal(err)
	}

	if err := checkIngestLeased(lctx, db, "test-1"); err == nil {
		t.Fatal("test-1 should not be leased after write")
	} else if !errdefs.IsNotFound(err) {
		t.Fatal("lease checked failed:", err)
	}
}

func createLease(ctx context.Context, db *DB, name string) (context.Context, func() error, error) {
	lm := NewLeaseManager(db)
	if _, err := lm.Create(ctx, leases.WithID(name)); err != nil {
		return nil, nil, err
	}
	return leases.WithLease(ctx, name), func() error {
		return lm.Delete(ctx, leases.Lease{
			ID: name,
		})
	}, nil
}

type hangingDeleteStore struct {
	content.Store
	deletes atomic.Int32
}

func (s *hangingDeleteStore) Delete(ctx context.Context, dgst digest.Digest) error {
	s.deletes.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

func TestGarbageCollectHangingDelete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, bdb := testEnv(t)

		lcs, err := local.NewStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		cs := &hangingDeleteStore{Store: lcs}
		db := NewDB(bdb, cs, nil)
		if err := db.Init(ctx); err != nil {
			t.Fatal(err)
		}

		// Orphan blobs: exist in the backend but not in metadata, so GC deletes them.
		var orphans []digest.Digest
		for _, data := range []string{"orphan-1", "orphan-2"} {
			blob := []byte(data)
			dgst := digest.FromBytes(blob)
			if err := content.WriteBlob(ctx, lcs, data, bytes.NewReader(blob),
				ocispec.Descriptor{Size: int64(len(blob)), Digest: dgst}); err != nil {
				t.Fatal(err)
			}
			orphans = append(orphans, dgst)
		}

		// The fake clock reaches the delete deadline as soon as Delete blocks,
		// so this runs against the real default instead of a shortened one. An
		// unbounded Delete would deadlock the bubble rather than pass.
		mcs := db.ContentStore().(*contentStore)
		_, err = mcs.garbageCollect(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected the bounded Delete to fail with DeadlineExceeded, got %v", err)
		}

		if n := cs.deletes.Load(); n != 1 {
			t.Fatalf("expected 1 delete attempt before abandoning the pass, got %d", n)
		}
		for _, dgst := range orphans {
			if _, err := lcs.Info(ctx, dgst); err != nil {
				t.Fatalf("orphan %q should survive for the next GC pass: %v", dgst, err)
			}
		}
	})
}

type hangingAbortStore struct {
	content.Store
	aborts atomic.Int32
}

func (s *hangingAbortStore) Abort(ctx context.Context, ref string) error {
	s.aborts.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

func TestGarbageCollectHangingAbort(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, bdb := testEnv(t)

		lcs, err := local.NewStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		cs := &hangingAbortStore{Store: lcs}
		db := NewDB(bdb, cs, nil)
		if err := db.Init(ctx); err != nil {
			t.Fatal(err)
		}

		// Commit one blob so the backend's blobs directory exists and the
		// Walk phase can run; it is unreferenced, so GC deletes it (through
		// the real local Delete) before reaching the ingest phase.
		blob := []byte("walked")
		if err := content.WriteBlob(ctx, lcs, "walked", bytes.NewReader(blob),
			ocispec.Descriptor{Size: int64(len(blob)), Digest: digest.FromBytes(blob)}); err != nil {
			t.Fatal(err)
		}

		// Orphan ingests: open in the backend but unknown to metadata, so GC
		// aborts them. The wrapper does not promote WalkStatusRefs, so this
		// exercises the ListStatuses fallback.
		refs := []string{"orphan-ingest-1", "orphan-ingest-2"}
		for _, ref := range refs {
			w, err := lcs.Writer(ctx, content.WithRef(ref))
			if err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
		}

		mcs := db.ContentStore().(*contentStore)
		_, err = mcs.garbageCollect(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected the bounded Abort to fail with DeadlineExceeded, got %v", err)
		}

		if n := cs.aborts.Load(); n != 1 {
			t.Fatalf("expected 1 abort attempt before abandoning the pass, got %d", n)
		}
		statuses, err := lcs.ListStatuses(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(statuses) != len(refs) {
			t.Fatalf("expected %d ingests to survive for the next GC pass, got %d", len(refs), len(statuses))
		}
	})
}

func checkContentLeased(ctx context.Context, db *DB, dgst digest.Digest) error {
	ns, ok := namespaces.Namespace(ctx)
	if !ok {
		return errors.New("no namespace in context")
	}
	lease, ok := leases.FromContext(ctx)
	if !ok {
		return errors.New("no lease in context")
	}

	return db.View(func(tx *bolt.Tx) error {
		bkt := getBucket(tx, bucketKeyVersion, []byte(ns), bucketKeyObjectLeases, []byte(lease), bucketKeyObjectContent)
		if bkt == nil {
			return fmt.Errorf("bucket not found %s: %w", lease, errdefs.ErrNotFound)
		}
		v := bkt.Get([]byte(dgst.String()))
		if v == nil {
			return fmt.Errorf("object not leased: %w", errdefs.ErrNotFound)
		}

		return nil
	})
}

func checkIngestLeased(ctx context.Context, db *DB, ref string) error {
	ns, ok := namespaces.Namespace(ctx)
	if !ok {
		return errors.New("no namespace in context")
	}
	lease, ok := leases.FromContext(ctx)
	if !ok {
		return errors.New("no lease in context")
	}

	return db.View(func(tx *bolt.Tx) error {
		bkt := getBucket(tx, bucketKeyVersion, []byte(ns), bucketKeyObjectLeases, []byte(lease), bucketKeyObjectIngests)
		if bkt == nil {
			return fmt.Errorf("bucket not found %s: %w", lease, errdefs.ErrNotFound)
		}
		v := bkt.Get([]byte(ref))
		if v == nil {
			return fmt.Errorf("object not leased: %w", errdefs.ErrNotFound)
		}

		return nil
	})
}
