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
	_ "crypto/sha256" // required for digest package
	_ "crypto/sha512" // required for sha384 and sha512 digest support
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

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

// TestContentWriterDigestAlgorithm verifies that content.WithBlobDigestAlgorithm
// is forwarded through the metadata content store wrapper on the eager open
// path that the walking differ uses. Without forwarding, a caller asking for
// e.g. sha512 receives a writer that silently hashed with sha256, leading to
// a compressed-blob digest in the wrong algorithm.
//
// The deferred (shared-namespace createAndCopy) path also forwards the
// algorithm, but that is a performance fix only: the re-hash safety net in
// the local store's Commit already preserves correctness for that flow, so it
// is not directly exercised here.
func TestContentWriterDigestAlgorithm(t *testing.T) {
	ctx, db := testDB(t)
	cs := db.ContentStore()

	for _, algo := range []digest.Algorithm{digest.SHA256, digest.SHA384, digest.SHA512} {
		t.Run(algo.String(), func(t *testing.T) {
			blob := bytes.Repeat([]byte(algo.String()[0:1]), 1024)
			expected := algo.FromBytes(blob)

			cw, err := cs.Writer(ctx,
				content.WithRef("alg-"+algo.String()),
				content.WithBlobDigestAlgorithm(algo),
			)
			if err != nil {
				t.Fatal(err)
			}
			defer cw.Close()
			if _, err := cw.Write(blob); err != nil {
				t.Fatal(err)
			}
			if got := cw.Digest(); got != expected {
				t.Fatalf("metadata wrapper dropped the algorithm: got %s, want %s", got, expected)
			}
			if err := cw.Commit(ctx, int64(len(blob)), ""); err != nil {
				t.Fatal(err)
			}
		})
	}
}
