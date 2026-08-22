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

package content

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	localcontent "github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	bolt "go.etcd.io/bbolt"
)

func TestIngestContentLease(t *testing.T) {
	t.Run("protects until durable root is established", func(t *testing.T) {
		ctx, db, cs, lm := newIngestStore(t)
		data := []byte("temporarily protected content")
		desc := descriptorFromBytes(data)
		ingestStartedAt := time.Now()

		require.NoError(t, ingestContent(ctx, cs, lm, defaultIngestLeaseDuration, "protected", bytes.NewReader(data), desc))
		ingestFinishedAt := time.Now()
		lease := requireSingleLease(t, ctx, lm)
		expiresAt, err := time.Parse(time.RFC3339Nano, lease.Labels["containerd.io/gc.expire"])
		require.NoError(t, err)
		// Lease expiration is recorded with one-second precision. Bound creation by
		// timestamps on both sides of ingest so service or scheduler latency cannot
		// make this assertion flaky.
		require.False(t, expiresAt.Before(ingestStartedAt.Add(defaultIngestLeaseDuration-time.Second)))
		require.False(t, expiresAt.After(ingestFinishedAt.Add(defaultIngestLeaseDuration+time.Second)))
		require.Equal(t, "true", lease.Labels[ingestLeaseLabel])
		require.Equal(t, "protected", lease.Labels[ingestLeaseRefLabel])

		_, err = db.GarbageCollect(ctx)
		require.NoError(t, err)
		info, err := cs.Info(ctx, desc.Digest)
		require.NoError(t, err, "temporary lease should protect content from GC")

		info.Labels = map[string]string{
			"containerd.io/gc.root": time.Now().Format(time.RFC3339Nano),
		}
		_, err = cs.Update(ctx, info, "labels.containerd.io/gc.root")
		require.NoError(t, err)
		require.NoError(t, lm.Delete(ctx, lease, leases.SynchronousDelete))

		_, err = db.GarbageCollect(ctx)
		require.NoError(t, err)
		_, err = cs.Info(ctx, desc.Digest)
		require.NoError(t, err, "durable GC root should protect content after the temporary lease is removed")
	})

	t.Run("zero duration leaves content unleased", func(t *testing.T) {
		ctx, db, cs, lm := newIngestStore(t)
		data := []byte("unleased content")
		desc := descriptorFromBytes(data)

		require.NoError(t, ingestContent(ctx, cs, lm, 0, "unleased", bytes.NewReader(data), desc))
		listed, err := lm.List(ctx)
		require.NoError(t, err)
		require.Empty(t, listed)

		_, err = db.GarbageCollect(ctx)
		require.NoError(t, err)
		_, err = cs.Info(ctx, desc.Digest)
		require.ErrorIs(t, err, errdefs.ErrNotFound)
	})

	t.Run("content is collectible after temporary protection is removed", func(t *testing.T) {
		ctx, db, cs, lm := newIngestStore(t)
		data := []byte("eventually collectible content")
		desc := descriptorFromBytes(data)

		require.NoError(t, ingestContent(ctx, cs, lm, defaultIngestLeaseDuration, "collectible", bytes.NewReader(data), desc))
		lease := requireSingleLease(t, ctx, lm)
		require.NoError(t, lm.Delete(ctx, lease, leases.SynchronousDelete))

		_, err := db.GarbageCollect(ctx)
		require.NoError(t, err)
		_, err = cs.Info(ctx, desc.Digest)
		require.ErrorIs(t, err, errdefs.ErrNotFound)
	})

	t.Run("failed ingest removes temporary lease", func(t *testing.T) {
		ctx, _, cs, lm := newIngestStore(t)
		data := []byte("content with the wrong digest")
		desc := descriptorFromBytes([]byte("different content"))
		desc.Size = int64(len(data))

		err := ingestContent(ctx, cs, lm, defaultIngestLeaseDuration, "failed", bytes.NewReader(data), desc)
		require.Error(t, err)
		listed, err := lm.List(ctx)
		require.NoError(t, err)
		require.Empty(t, listed)
	})

	t.Run("canceled ingest removes temporary lease", func(t *testing.T) {
		ctx, _, cs, lm := newIngestStore(t)
		data := []byte("canceled content")
		desc := descriptorFromBytes(data)
		canceledCtx, cancel := context.WithCancel(ctx)

		err := ingestContent(canceledCtx, cs, lm, defaultIngestLeaseDuration, "canceled", cancelingReader{cancel: cancel}, desc)
		require.ErrorIs(t, err, context.Canceled)
		listed, err := lm.List(ctx)
		require.NoError(t, err)
		require.Empty(t, listed)
	})
}

func TestIngestLeaseDurationDefault(t *testing.T) {
	for _, flag := range ingestCommand.Flags {
		for _, name := range flag.Names() {
			if name != "lease-duration" {
				continue
			}
			durationFlag, ok := flag.(*cli.DurationFlag)
			if !ok {
				t.Fatalf("lease-duration flag has type %T, want *cli.DurationFlag", flag)
			}
			require.Equal(t, defaultIngestLeaseDuration, durationFlag.Value)
			return
		}
	}
	t.Fatal("lease-duration flag not found")
}

func TestIngestLeaseDurationValidation(t *testing.T) {
	err := ingestContent(t.Context(), nil, nil, -time.Second, "negative", bytes.NewReader(nil), ocispec.Descriptor{})
	require.EqualError(t, err, "--lease-duration must be >= 0, got -1s")
}

func newIngestStore(t *testing.T) (context.Context, *metadata.DB, content.Store, leases.Manager) {
	t.Helper()

	ctx := namespaces.WithNamespace(t.Context(), "testing")
	dir := t.TempDir()
	localStore, err := localcontent.NewStore(filepath.Join(dir, "content"))
	require.NoError(t, err)
	bdb, err := bolt.Open(filepath.Join(dir, "metadata.db"), 0600, nil)
	require.NoError(t, err)
	db := metadata.NewDB(bdb, localStore, nil)
	require.NoError(t, db.Init(ctx))
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	return ctx, db, db.ContentStore(), metadata.NewLeaseManager(db)
}

func descriptorFromBytes(data []byte) ocispec.Descriptor {
	return ocispec.Descriptor{
		Digest: digest.FromBytes(data),
		Size:   int64(len(data)),
	}
}

func requireSingleLease(t *testing.T, ctx context.Context, lm leases.Manager) leases.Lease {
	t.Helper()
	listed, err := lm.List(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	return listed[0]
}

type cancelingReader struct {
	cancel context.CancelFunc
}

func (r cancelingReader) Read([]byte) (int, error) {
	r.cancel()
	return 0, context.Canceled
}
