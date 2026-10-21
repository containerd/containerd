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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/content/testsuite"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/metadata/boltutil"
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

// precommitStore instruments preparation without requiring an fs-verity capable
// filesystem. Publication still goes through the real local content store.
type precommitStore struct {
	content.Store
	prepare func(context.Context, int64, digest.Digest) error
}

func (s *precommitStore) Writer(ctx context.Context, opts ...content.WriterOpt) (content.Writer, error) {
	w, err := s.Store.Writer(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &precommitWriter{Writer: w, prepare: s.prepare}, nil
}

type precommitWriter struct {
	content.Writer
	prepare func(context.Context, int64, digest.Digest) error
}

func (w *precommitWriter) Precommit(ctx context.Context, size int64, expected digest.Digest) error {
	if err := w.prepare(ctx, size, expected); err != nil {
		w.Close()
		return err
	}
	if p, ok := w.Writer.(content.Precommitter); ok {
		return p.Precommit(ctx, size, expected)
	}
	return nil
}

func TestContentPrecommitOutsideTransaction(t *testing.T) {
	ctx, db := testDB(t)
	cs := db.ContentStore().(*contentStore)
	backend := cs.Store
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	cs.Store = &precommitStore{Store: backend, prepare: func(context.Context, int64, digest.Digest) error {
		close(entered)
		<-release
		return nil
	}}

	blob := []byte("prepare before metadata transaction")
	dgst := digest.FromBytes(blob)
	w, err := cs.Writer(ctx, content.WithRef("precommit"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Write(blob); err != nil {
		t.Fatal(err)
	}
	committed := make(chan error, 1)
	go func() { committed <- w.Commit(ctx, int64(len(blob)), dgst) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("commit did not enter preparation")
	}

	// A completely unrelated write must finish while preparation is blocked.
	updated := make(chan error, 1)
	go func() {
		updated <- db.Update(func(tx *bolt.Tx) error {
			_, err := tx.CreateBucketIfNotExists([]byte("precommit-test"))
			return err
		})
	}()
	select {
	case err := <-updated:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		unblock()
		<-committed
		<-updated
		t.Fatal("preparation blocked an unrelated metadata write")
	}
	if _, err := backend.Info(ctx, dgst); !errdefs.IsNotFound(err) {
		t.Errorf("backend content published before preparation finished: %v", err)
	}
	if _, err := cs.Info(ctx, dgst); !errdefs.IsNotFound(err) {
		t.Errorf("namespace content published before preparation finished: %v", err)
	}
	if other, err := cs.Writer(ctx, content.WithRef("precommit")); !errdefs.IsUnavailable(err) {
		if other != nil {
			other.Close()
		}
		t.Errorf("ingest ownership lost during preparation: %v", err)
	}
	unblock()
	if err := <-committed; err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Info(ctx, dgst); err != nil {
		t.Fatal(err)
	}
}

func TestContentPrecommitValidation(t *testing.T) {
	for _, tc := range []string{"options", "labels", "size", "prepare"} {
		t.Run(tc, func(t *testing.T) {
			ctx, db := testDB(t)
			cs := db.ContentStore().(*contentStore)
			var prepared bool
			prepareErr := errors.New("preparation failed")
			cs.Store = &precommitStore{Store: cs.Store, prepare: func(context.Context, int64, digest.Digest) error {
				prepared = true
				return prepareErr
			}}
			w, err := cs.Writer(ctx, content.WithRef("validation"))
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			blob := []byte("retain upload on validation failure")
			if _, err := w.Write(blob); err != nil {
				t.Fatal(err)
			}
			size := int64(len(blob))
			var opts []content.Opt
			switch tc {
			case "options":
				opts = append(opts, func(*content.Info) error { return errors.New("invalid option") })
			case "labels":
				opts = append(opts, content.WithLabels(map[string]string{"invalid": strings.Repeat("x", 4096)}))
			case "size":
				size++
			}
			if err := w.Commit(ctx, size, digest.FromBytes(blob), opts...); err == nil {
				t.Fatal("expected commit failure")
			}
			if prepared != (tc == "prepare") {
				t.Fatalf("unexpected preparation: %v", prepared)
			}
			resumed, err := cs.Writer(ctx, content.WithRef("validation"))
			if err != nil {
				t.Fatal(err)
			}
			defer resumed.Close()
			st, err := resumed.Status()
			if err != nil {
				t.Fatal(err)
			}
			if st.Offset != int64(len(blob)) {
				t.Fatalf("lost upload progress: %d", st.Offset)
			}
		})
	}
}

func TestContentPrecommitAbort(t *testing.T) {
	ctx, db := testDB(t)
	cs := db.ContentStore().(*contentStore)
	backend := cs.Store
	blob := []byte("existing backend content")
	dgst := digest.FromBytes(blob)
	// A duplicate backend makes publication return AlreadyExists even if the
	// old ingest was removed. It must not hide a concurrent abort.
	if err := content.WriteBlob(ctx, backend, "seed", bytes.NewReader(blob), ocispec.Descriptor{Digest: dgst, Size: int64(len(blob))}); err != nil {
		t.Fatal(err)
	}
	cs.Store = &precommitStore{Store: backend, prepare: func(context.Context, int64, digest.Digest) error {
		return cs.Abort(ctx, "reuse")
	}}
	old, err := cs.Writer(ctx, content.WithRef("reuse"))
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if _, err := old.Write(blob); err != nil {
		t.Fatal(err)
	}
	// This wrapper intentionally leaves preparation to the instrumented hook:
	// the aborted backend file no longer exists, but duplicate publication can
	// still succeed if the metadata transaction fails to check the abort.
	nw := old.(*namespacedWriter)
	nw.w = &abortPrecommitWriter{Writer: nw.w, prepare: cs.Store.(*precommitStore).prepare}
	if err := old.Commit(ctx, int64(len(blob)), dgst); !errdefs.IsNotFound(err) {
		t.Fatalf("stale ingest commit returned %v", err)
	}
	if _, err := cs.Status(ctx, "reuse"); !errdefs.IsNotFound(err) {
		t.Fatalf("aborted ingest metadata reappeared: %v", err)
	}
	if _, err := cs.Info(ctx, dgst); !errdefs.IsNotFound(err) {
		t.Fatalf("aborted content published into namespace: %v", err)
	}
}

type abortPrecommitWriter struct {
	content.Writer
	prepare func(context.Context, int64, digest.Digest) error
}

func (w *abortPrecommitWriter) Precommit(ctx context.Context, size int64, expected digest.Digest) error {
	return w.prepare(ctx, size, expected)
}

func TestContentPrecommitTransactionFailure(t *testing.T) {
	ctx, db := testDB(t)
	cs := db.ContentStore().(*contentStore)
	// Force publication to fail after preparation, while leaving the ingest
	// available. A new writer must recover its bytes even if verity sealed it.
	root := t.TempDir()
	backend, err := local.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	cs.Store = backend
	blob := []byte("resume after failed publication")
	dgst := digest.FromBytes(blob)
	w, err := cs.Writer(ctx, content.WithRef("recovery"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Write(blob); err != nil {
		t.Fatal(err)
	}
	// A file in place of the blobs directory makes the publication fail.
	if err := os.WriteFile(filepath.Join(root, "blobs"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(ctx, int64(len(blob)), dgst); err == nil {
		t.Fatal("expected publication failure")
	}
	if err := os.Remove(filepath.Join(root, "blobs")); err != nil {
		t.Fatal(err)
	}
	resumed, err := cs.Writer(ctx, content.WithRef("recovery"))
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	st, err := resumed.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Offset != int64(len(blob)) {
		t.Fatalf("lost upload progress: %d", st.Offset)
	}
	if err := resumed.Commit(ctx, int64(len(blob)), dgst); err != nil {
		t.Fatal(err)
	}
}

func TestContentPrecommitOptionsOnce(t *testing.T) {
	ctx, db := testDB(t)
	cs := db.ContentStore()
	blob := []byte("evaluate commit options once")
	w, err := cs.Writer(ctx, content.WithRef("options"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Write(blob); err != nil {
		t.Fatal(err)
	}
	var calls int
	if err := w.Commit(ctx, int64(len(blob)), digest.FromBytes(blob), func(info *content.Info) error {
		calls++
		info.Labels = map[string]string{"test": "value"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("evaluated commit option %d times", calls)
	}
	info, err := cs.Info(ctx, digest.FromBytes(blob))
	if err != nil {
		t.Fatal(err)
	}
	if info.Labels["test"] != "value" {
		t.Fatalf("lost commit labels: %v", info.Labels)
	}
}

func TestContentPrecommitCallerTransaction(t *testing.T) {
	for _, writable := range []bool{false, true} {
		t.Run(fmt.Sprintf("writable=%v", writable), func(t *testing.T) {
			ctx, db := testDB(t)
			cs := db.ContentStore().(*contentStore)
			cs.Store = &precommitStore{Store: cs.Store, prepare: func(context.Context, int64, digest.Digest) error {
				t.Error("preparation must not run ahead of a caller-owned transaction")
				return nil
			}}
			blob := []byte("caller-owned transaction")
			dgst := digest.FromBytes(blob)
			w, err := cs.Writer(ctx, content.WithRef("caller"))
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			if _, err := w.Write(blob); err != nil {
				t.Fatal(err)
			}
			commit := func(tx *bolt.Tx) error {
				return w.Commit(boltutil.WithTransaction(ctx, tx), int64(len(blob)), dgst)
			}
			if writable {
				if err := db.Update(commit); err != nil {
					t.Fatal(err)
				}
				if _, err := cs.Info(ctx, dgst); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := db.View(commit); err == nil {
					t.Fatal("commit with a read-only transaction succeeded")
				}
				if _, err := cs.Store.Info(ctx, dgst); !errdefs.IsNotFound(err) {
					t.Fatalf("read-only transaction published backend content: %v", err)
				}
			}
		})
	}
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
