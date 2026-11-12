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

package remotes

import (
	"bytes"
	"context"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	bolt "go.etcd.io/bbolt"
)

func TestContextCustomKeyPrefix(t *testing.T) {
	ctx := context.Background()
	cmt := "testing/custom.media.type"
	ctx = WithMediaTypeKeyPrefix(ctx, images.MediaTypeDockerSchema2Layer, "bananas")
	ctx = WithMediaTypeKeyPrefix(ctx, cmt, "apples")

	// makes sure that even though we've supplied some custom handling, the built-in still works
	t.Run("normal supported case", func(t *testing.T) {
		desc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayer}
		expected := "layer-"

		actual := MakeRefKey(ctx, desc)
		if actual != expected {
			t.Fatalf("unexpected ref key, expected %s, got: %s", expected, actual)
		}
	})

	t.Run("unknown media type", func(t *testing.T) {
		desc := ocispec.Descriptor{MediaType: "we.dont.know.what.this.is"}
		expected := "unknown-"

		actual := MakeRefKey(ctx, desc)
		if actual != expected {
			t.Fatalf("unexpected ref key, expected %s, got: %s", expected, actual)
		}
	})

	t.Run("overwrite supported media type", func(t *testing.T) {
		desc := ocispec.Descriptor{MediaType: images.MediaTypeDockerSchema2Layer}
		expected := "bananas-"

		actual := MakeRefKey(ctx, desc)
		if actual != expected {
			t.Fatalf("unexpected ref key, expected %s, got: %s", expected, actual)
		}
	})

	t.Run("custom media type", func(t *testing.T) {
		desc := ocispec.Descriptor{MediaType: cmt}
		expected := "apples-"

		actual := MakeRefKey(ctx, desc)
		if actual != expected {
			t.Fatalf("unexpected ref key, expected %s, got: %s", expected, actual)
		}
	})
}

//nolint:staticcheck // Non-distributable layers are deprecated
func TestSkipNonDistributableBlobs(t *testing.T) {
	ctx := context.Background()

	out, err := SkipNonDistributableBlobs(images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		return []ocispec.Descriptor{
			{MediaType: images.MediaTypeDockerSchema2Layer, Digest: "test:1"},
			{MediaType: images.MediaTypeDockerSchema2LayerForeign, Digest: "test:2"},
			{MediaType: images.MediaTypeDockerSchema2LayerForeignGzip, Digest: "test:3"},
			{MediaType: ocispec.MediaTypeImageLayerNonDistributable, Digest: "test:4"},
			{MediaType: ocispec.MediaTypeImageLayerNonDistributableGzip, Digest: "test:5"},
			{MediaType: ocispec.MediaTypeImageLayerNonDistributableZstd, Digest: "test:6"},
		}, nil
	}))(ctx, ocispec.Descriptor{MediaType: images.MediaTypeDockerSchema2Manifest})
	if err != nil {
		t.Fatal(err)
	}

	if len(out) != 1 {
		t.Fatalf("unexpected number of descriptors returned: %d", len(out))
	}
	if out[0].Digest != "test:1" {
		t.Fatalf("unexpected digest returned: %s", out[0].Digest)
	}

	dir := t.TempDir()
	cs, err := local.NewLabeledStore(dir, newMemoryLabelStore())
	if err != nil {
		t.Fatal(err)
	}

	write := func(i any, ref string) digest.Digest {
		t.Helper()

		data, err := json.Marshal(i)
		if err != nil {
			t.Fatal(err)
		}

		w, err := cs.Writer(ctx, content.WithRef(ref))
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()

		dgst := digest.SHA256.FromBytes(data)

		n, err := w.Write(data)
		if err != nil {
			t.Fatal(err)
		}

		if err := w.Commit(ctx, int64(n), dgst); err != nil {
			t.Fatal(err)
		}

		return dgst
	}

	configDigest := write(ocispec.ImageConfig{}, "config")

	manifest := ocispec.Manifest{
		Config:    ocispec.Descriptor{Digest: configDigest, MediaType: ocispec.MediaTypeImageConfig},
		MediaType: ocispec.MediaTypeImageManifest,
		Layers: []ocispec.Descriptor{
			{MediaType: images.MediaTypeDockerSchema2Layer, Digest: "test:1"},
			{MediaType: images.MediaTypeDockerSchema2LayerForeign, Digest: "test:2"},
			{MediaType: images.MediaTypeDockerSchema2LayerForeignGzip, Digest: "test:3"},
			{MediaType: ocispec.MediaTypeImageLayerNonDistributable, Digest: "test:4"},
			{MediaType: ocispec.MediaTypeImageLayerNonDistributableGzip, Digest: "test:5"},
			{MediaType: ocispec.MediaTypeImageLayerNonDistributableZstd, Digest: "test:6"},
		},
	}

	manifestDigest := write(manifest, "manifest")

	out, err = SkipNonDistributableBlobs(images.ChildrenHandler(cs))(ctx, ocispec.Descriptor{MediaType: manifest.MediaType, Digest: manifestDigest})
	if err != nil {
		t.Fatal(err)
	}

	if len(out) != 2 {
		t.Fatalf("unexpected number of descriptors returned: %v", out)
	}

	if out[0].Digest != configDigest {
		t.Fatalf("unexpected digest returned: %v", out[0])
	}
	if out[1].Digest != manifest.Layers[0].Digest {
		t.Fatalf("unexpected digest returned: %v", out[1])
	}
}

type memoryLabelStore struct {
	l      sync.Mutex
	labels map[digest.Digest]map[string]string
}

func newMemoryLabelStore() local.LabelStore {
	return &memoryLabelStore{
		labels: map[digest.Digest]map[string]string{},
	}
}

func (mls *memoryLabelStore) Get(d digest.Digest) (map[string]string, error) {
	mls.l.Lock()
	labels := mls.labels[d]
	mls.l.Unlock()

	return labels, nil
}

func (mls *memoryLabelStore) Set(d digest.Digest, labels map[string]string) error {
	mls.l.Lock()
	mls.labels[d] = labels
	mls.l.Unlock()

	return nil
}

func (mls *memoryLabelStore) Update(d digest.Digest, update map[string]string) (map[string]string, error) {
	mls.l.Lock()
	labels, ok := mls.labels[d]
	if !ok {
		labels = map[string]string{}
	}
	for k, v := range update {
		if v == "" {
			delete(labels, k)
		} else {
			labels[k] = v
		}
	}
	mls.labels[d] = labels
	mls.l.Unlock()

	return labels, nil
}

func TestFetchSizeValidation(t *testing.T) {
	ctx := t.Context()
	cs, err := local.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	payload := bytes.Repeat([]byte("A"), 121)
	dgst := digest.FromBytes(payload)

	for _, tc := range []struct {
		name string
		size int64
	}{
		{name: "zero size", size: 0},
		{name: "negative size -1", size: -1},
		{name: "negative size -100", size: -100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			fetcher := FetcherFunc(func(context.Context, ocispec.Descriptor) (io.ReadCloser, error) {
				called = true
				return io.NopCloser(bytes.NewReader(payload)), nil
			})
			desc := ocispec.Descriptor{
				MediaType: ocispec.MediaTypeImageLayer,
				Digest:    dgst,
				Size:      tc.size,
			}
			err := Fetch(ctx, cs, fetcher, desc)
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Fatalf("expected ErrInvalidArgument, got %v", err)
			}
			if called {
				t.Fatal("fetcher should not be called when descriptor size is non-positive")
			}
		})
	}

	t.Run("oversized stream fails before commit", func(t *testing.T) {
		var bytesServed int64
		fetcher := FetcherFunc(func(context.Context, ocispec.Descriptor) (io.ReadCloser, error) {
			return io.NopCloser(readerFunc(func(p []byte) (int, error) {
				for i := range p {
					p[i] = 'A'
				}
				bytesServed += int64(len(p))
				return len(p), nil
			})), nil
		})
		desc := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageLayer,
			Digest:    dgst,
			Size:      int64(len(payload)),
		}
		err := Fetch(ctx, cs, fetcher, desc)
		if !errors.Is(err, errdefs.ErrFailedPrecondition) {
			t.Fatalf("expected ErrFailedPrecondition, got %v", err)
		}
		if bytesServed != int64(len(payload))+1 {
			t.Fatalf("expected %d bytes read from stream, got %d", len(payload)+1, bytesServed)
		}
		if _, err := cs.Info(ctx, dgst); !errors.Is(err, errdefs.ErrNotFound) {
			t.Fatalf("expected blob not to be committed, got %v", err)
		}
	})
}

func newMetadataContentStore(t *testing.T, ctx context.Context) content.Store {
	t.Helper()
	dirname := t.TempDir()
	cs, err := local.NewStore(filepath.Join(dirname, "content"))
	if err != nil {
		t.Fatal(err)
	}
	bdb, err := bolt.Open(filepath.Join(dirname, "metadata.db"), 0644, nil)
	if err != nil {
		t.Fatal(err)
	}
	db := metadata.NewDB(bdb, cs, nil)
	if err := db.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db.ContentStore()
}

// TestFetchAbortsIngestOnDigestMismatch verifies that a completed ingest whose
// stored data does not match the descriptor digest is rejected by Commit and
// aborted, so a subsequent fetch with the same ref re-downloads the content
// instead of short-circuiting into the same failing commit.
func TestFetchAbortsIngestOnDigestMismatch(t *testing.T) {
	ctx := t.Context()
	ctx = namespaces.WithNamespace(ctx, "testing")
	cs := newMetadataContentStore(t, ctx)

	payload := bytes.Repeat([]byte("A"), 34)
	stored := bytes.Repeat([]byte("B"), 34) // same length as payload, different digest
	dgst := digest.SHA512.FromBytes(payload)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    dgst,
		Size:      int64(len(payload)),
	}
	ref := MakeRefKey(ctx, desc)

	// Pre-seed a complete, uncommitted ingest whose data does NOT match dgst.
	w, err := cs.Writer(ctx, content.WithRef(ref), content.WithDescriptor(desc))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(stored); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	fetches := 0
	fetcher := FetcherFunc(func(context.Context, ocispec.Descriptor) (io.ReadCloser, error) {
		fetches++
		return io.NopCloser(bytes.NewReader(payload)), nil
	})

	// Commit must confirm the digest mismatch and the ingest must be aborted.
	if err := Fetch(ctx, cs, fetcher, desc); !errors.Is(err, errdefs.ErrFailedPrecondition) {
		t.Fatalf("expected ErrFailedPrecondition, got %v", err)
	}

	// A subsequent fetch with the same ref must re-download and succeed.
	if err := Fetch(ctx, cs, fetcher, desc); err != nil {
		t.Fatalf("expected retried fetch to succeed, got %v", err)
	}
	if fetches != 1 {
		t.Fatalf("expected fetcher to be called once after aborting the stale ingest, got %d calls", fetches)
	}
	if info, err := cs.Info(ctx, dgst); err != nil {
		t.Fatalf("expected SHA-512 blob to be committed, got %v", err)
	} else if info.Digest != dgst {
		t.Fatalf("unexpected committed digest %v, expected %v", info.Digest, dgst)
	}
}

// TestFetchSerializesSameRef verifies that concurrent fetches of the same ref
// are serialized by the per-ref fetch lock, so a failed commit's stale-ingest
// abort can never delete a replacement ingest created by a concurrent fetch of
// the same content.
func TestFetchSerializesSameRef(t *testing.T) {
	ctx := t.Context()
	ctx = namespaces.WithNamespace(ctx, "testing")
	cs := newMetadataContentStore(t, ctx)

	payload := bytes.Repeat([]byte("A"), 34)
	dgst := digest.SHA512.FromBytes(payload)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    dgst,
		Size:      int64(len(payload)),
	}

	// Block the first fetch inside the fetcher (it holds the per-ref lock), so
	// we can observe that a second fetch of the same ref waits for it.
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var first sync.Once
	fetcher := FetcherFunc(func(context.Context, ocispec.Descriptor) (io.ReadCloser, error) {
		first.Do(func() {
			close(firstEntered)
			<-releaseFirst
		})
		return io.NopCloser(bytes.NewReader(payload)), nil
	})

	done := make(chan error, 2)
	go func() {
		done <- Fetch(ctx, cs, fetcher, desc)
	}()
	<-firstEntered

	// A second fetch of the same ref must block behind the first.
	go func() {
		done <- Fetch(ctx, cs, fetcher, desc)
	}()
	select {
	case err := <-done:
		t.Fatalf("second fetch ran concurrently with the first (per-ref lock not effective): %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	close(releaseFirst)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil && !errdefs.IsAlreadyExists(err) {
			t.Fatal(err)
		}
	}

	// The content must have been committed (by the first fetch; the second
	// fetch recognizes it already exists).
	if info, err := cs.Info(ctx, dgst); err != nil {
		t.Fatalf("expected blob to be committed, got %v", err)
	} else if info.Digest != dgst {
		t.Fatalf("unexpected committed digest %v, expected %v", info.Digest, dgst)
	}
}

type ownershipMock struct {
	content.Store
	otherStartedAt time.Time
	aborted        *atomic.Bool
}

func (m *ownershipMock) Status(ctx context.Context, ref string) (content.Status, error) {
	st, err := m.Store.Status(ctx, ref)
	if err == nil {
		st.StartedAt = m.otherStartedAt
	}
	return st, err
}

func (m *ownershipMock) Abort(ctx context.Context, ref string) error {
	m.aborted.Store(true)
	return m.Store.Abort(ctx, ref)
}

// TestFetchDoesNotAbortReplacementOwnedByOtherClient verifies that the stale
// ingest cleanup after a failed commit only aborts the very ingest whose commit
// failed. If the ref now holds a different ingest (e.g. started by another
// client through a proxy content store), cleanup must leave it alone.
func TestFetchDoesNotAbortReplacementOwnedByOtherClient(t *testing.T) {
	ctx := t.Context()
	ctx = namespaces.WithNamespace(ctx, "testing")
	cs := newMetadataContentStore(t, ctx)

	payload := bytes.Repeat([]byte("A"), 34)
	stored := bytes.Repeat([]byte("B"), 34)
	dgst := digest.SHA512.FromBytes(payload)
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    dgst,
		Size:      int64(len(payload)),
	}
	ref := MakeRefKey(ctx, desc)

	// Seed a complete, uncommitted ingest whose data does NOT match dgst.
	w, err := cs.Writer(ctx, content.WithRef(ref), content.WithDescriptor(desc))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(stored); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Report the ref as owned by a different ingest: cleanup must not abort it.
	var aborted atomic.Bool
	client := &ownershipMock{Store: cs, otherStartedAt: time.Now().Add(time.Hour), aborted: &aborted}
	fetcher := FetcherFunc(func(context.Context, ocispec.Descriptor) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(payload)), nil
	})

	if err := Fetch(ctx, client, fetcher, desc); !errors.Is(err, errdefs.ErrFailedPrecondition) {
		t.Fatalf("expected ErrFailedPrecondition, got %v", err)
	}
	if aborted.Load() {
		t.Fatal("cleanup aborted an ingest owned by another client")
	}

	// The stale ingest must still be present.
	if st, err := cs.Status(ctx, ref); err != nil {
		t.Fatalf("expected stale ingest to remain, got %v", err)
	} else if st.Offset != desc.Size {
		t.Fatalf("unexpected stale ingest offset %d, expected %d", st.Offset, desc.Size)
	}
}

func TestPushSizeValidation(t *testing.T) {
	ctx := t.Context()

	for _, tc := range []struct {
		name string
		size int64
	}{
		{name: "negative size -1", size: -1},
		{name: "negative size -100", size: -100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			desc := ocispec.Descriptor{
				MediaType: ocispec.MediaTypeImageLayer,
				Digest:    digest.FromString("test"),
				Size:      tc.size,
			}

			err := push(ctx, nil, nil, desc)
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Fatalf("expected ErrInvalidArgument, got %v", err)
			}
		})
	}
}

type readerFunc func(p []byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }
