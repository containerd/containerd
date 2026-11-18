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
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/content/testsuite"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/metadata/boltutil"
	"github.com/containerd/containerd/v2/internal/fsverity"
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

// finalizeStore observes when backend finalization runs.
type finalizeStore struct {
	content.Store
	deferred bool
	finalize func(context.Context)
}

func (s *finalizeStore) Writer(ctx context.Context, opts ...content.WriterOpt) (content.Writer, error) {
	w, err := s.Store.Writer(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &finalizeWriter{Writer: w, s: s}, nil
}

func (s *finalizeStore) FinalizeBlob(ctx context.Context, _ digest.Digest) { s.finalize(ctx) }

type finalizeWriter struct {
	content.Writer
	s *finalizeStore
}

func (w *finalizeWriter) DeferFinalize() { w.s.deferred = true }

func TestContentFinalizeOutsideTransaction(t *testing.T) {
	for _, callerTransaction := range []bool{false, true} {
		t.Run(fmt.Sprintf("callerTransaction=%v", callerTransaction), func(t *testing.T) {
			ctx, db := testDB(t)
			cs := db.ContentStore().(*contentStore)
			blob := []byte("finalize after metadata transaction")
			dgst := digest.FromBytes(blob)
			finalized := make(chan struct{})
			cs.Store = &finalizeStore{Store: cs.Store, finalize: func(context.Context) {
				defer close(finalized)
				if _, err := cs.Info(ctx, dgst); err != nil {
					t.Errorf("content not committed before finalization: %v", err)
				}
				// Fails if finalization runs while the write transaction is open.
				done := make(chan error, 1)
				go func() {
					done <- db.Update(func(tx *bolt.Tx) error {
						_, err := tx.CreateBucketIfNotExists([]byte("finalize-test"))
						return err
					})
				}()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(10 * time.Second):
					t.Error("finalization blocked an unrelated metadata write")
				}
			}}

			w, err := cs.Writer(ctx, content.WithRef("finalize"))
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			if _, err := w.Write(blob); err != nil {
				t.Fatal(err)
			}
			commit := func(ctx context.Context) error {
				return w.Commit(ctx, int64(len(blob)), dgst)
			}
			if callerTransaction {
				err = db.Update(func(tx *bolt.Tx) error {
					if err := commit(boltutil.WithTransaction(ctx, tx)); err != nil {
						return err
					}
					select {
					case <-finalized:
						t.Error("finalized before the caller's transaction committed")
					default:
					}
					return nil
				})
			} else {
				err = commit(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-finalized:
			case <-time.After(20 * time.Second):
				t.Fatal("content was not finalized")
			}
			if !cs.Store.(*finalizeStore).deferred {
				t.Error("finalization was not deferred by the metadata writer")
			}
		})
	}
}

func TestContentSharedCommitFinalizesAfterRollback(t *testing.T) {
	ctx, db := testDB(t)
	cs := db.ContentStore().(*contentStore)
	root := t.TempDir()
	backend, err := local.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	cs.Store = backend
	cs.shared = true
	blob := []byte("published by a rolled back commit")
	desc := ocispec.Descriptor{Digest: digest.FromBytes(blob), Size: int64(len(blob))}

	w, err := cs.Writer(ctx, content.WithRef("rollback"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Write(blob); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("rollback")
	if err := db.Update(func(tx *bolt.Tx) error {
		if err := w.Commit(boltutil.WithTransaction(ctx, tx), desc.Size, desc.Digest); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("expected rollback, got %v", err)
	}

	// The backend blob exists, so the retry takes the shared path and never
	// reaches the backend commit.
	retry, err := cs.Writer(ctx, content.WithRef("rollback"), content.WithDescriptor(desc))
	if err != nil {
		t.Fatal(err)
	}
	defer retry.Close()
	if retry.(*namespacedWriter).w != nil {
		t.Fatal("expected a shared writer")
	}
	if err := retry.Commit(ctx, desc.Size, desc.Digest); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, "blobs", desc.Digest.Algorithm().String(), desc.Digest.Encoded())
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode()&0222 != 0 {
		t.Errorf("blob left writable: %v", fi.Mode())
	}
	if ok, _ := fsverity.IsSupported(filepath.Dir(path)); ok {
		if enabled, err := fsverity.IsEnabled(path); err != nil || !enabled {
			t.Errorf("blob not sealed: enabled=%v err=%v", enabled, err)
		}
	} else {
		t.Log("fsverity not supported, skipping seal check")
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
