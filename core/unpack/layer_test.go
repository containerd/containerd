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

package unpack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/imagetest"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/internal/kmutex"
)

// layerImage is an image of uncompressed layers. The diffID of each layer is
// its blob digest.
type layerImage struct {
	store    imagetest.ContentStore
	config   ocispec.Descriptor
	layers   []ocispec.Descriptor
	blobs    map[digest.Digest][]byte
	chainIDs []digest.Digest
}

func newLayerImage(t *testing.T, contents ...string) *layerImage {
	t.Helper()
	img := &layerImage{
		store: imagetest.NewContentStore(t.Context(), t),
		blobs: make(map[digest.Digest][]byte),
	}
	var diffIDs []digest.Digest
	for _, c := range contents {
		b := []byte(c)
		desc := blobDesc(ocispec.MediaTypeImageLayer, b)
		img.layers = append(img.layers, desc)
		img.blobs[desc.Digest] = b
		diffIDs = append(diffIDs, desc.Digest)
	}
	img.config = img.store.JSONObject(ocispec.MediaTypeImageConfig, ocispec.Image{
		Platform: platforms.DefaultSpec(),
		RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: diffIDs},
	}).Descriptor
	img.chainIDs = identity.ChainIDs(slices.Clone(diffIDs))
	return img
}

// handler returns a fetch handler that calls before and then writes the layer
// blob into the store. An error from before fails the fetch.
func (img *layerImage) handler(before func(ctx context.Context, desc ocispec.Descriptor) error) images.Handler {
	return images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		if before != nil {
			if err := before(ctx, desc); err != nil {
				return nil, err
			}
		}
		return nil, content.WriteBlob(ctx, img.store.Store, desc.Digest.String(), bytes.NewReader(img.blobs[desc.Digest]), desc)
	})
}

// applier returns an applier that calls before and then returns the digest of
// the layer blob as the diff. An error from before fails the apply.
func (img *layerImage) applier(before func(ctx context.Context, desc ocispec.Descriptor) error) diff.Applier {
	return applyFunc(func(ctx context.Context, desc ocispec.Descriptor) (ocispec.Descriptor, error) {
		if before != nil {
			if err := before(ctx, desc); err != nil {
				return ocispec.Descriptor{}, err
			}
		}
		b, err := content.ReadBlob(ctx, img.store.Store, desc)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		return ocispec.Descriptor{Digest: digest.FromBytes(b)}, nil
	})
}

type applyFunc func(context.Context, ocispec.Descriptor) (ocispec.Descriptor, error)

func (f applyFunc) Apply(ctx context.Context, desc ocispec.Descriptor, _ []mount.Mount, _ ...diff.ApplyOpt) (ocispec.Descriptor, error) {
	return f(ctx, desc)
}

// recordingSnapshotter returns read-only mounts for the layers whose diffIDs
// are in staged, and writable mounts for the others. It records the calls to
// Prepare, Commit, and Remove. Prepare and Commit return ErrNotFound for a
// parent that is not committed, as the metadata snapshotter does.
type recordingSnapshotter struct {
	snapshots.Snapshotter
	staged map[digest.Digest]bool

	mu       sync.Mutex
	prepares []call // name is the chain ID
	commits  []call
	removes  []string
}

func (s *recordingSnapshotter) checkParent(parent string) error {
	if parent == "" || slices.ContainsFunc(s.commits, func(c call) bool { return c.name == parent }) {
		return nil
	}
	return fmt.Errorf("parent snapshot %v does not exist: %w", parent, errdefs.ErrNotFound)
}

func (s *recordingSnapshotter) Prepare(_ context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	var info snapshots.Info
	for _, o := range opts {
		if err := o(&info); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.ContainsFunc(s.commits, func(c call) bool { return c.name == info.Labels[snapshots.LabelSnapshotRef] }) {
		return nil, errdefs.ErrAlreadyExists
	}
	s.prepares = append(s.prepares, call{name: info.Labels[snapshots.LabelSnapshotRef], key: key, parent: parent})
	if err := s.checkParent(parent); err != nil {
		return nil, err
	}
	if s.staged[digest.Digest(info.Labels[snapshots.LabelSnapshotDiffID])] {
		return []mount.Mount{{Type: "erofs", Source: "/staged/layer.erofs", Options: []string{"ro"}}}, nil
	}
	return []mount.Mount{{Type: "bind", Source: "/active", Options: []string{"rbind"}}}, nil
}

func (s *recordingSnapshotter) Commit(_ context.Context, name, key string, opts ...snapshots.Opt) error {
	var info snapshots.Info
	for _, o := range opts {
		if err := o(&info); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkParent(info.Parent); err != nil {
		return err
	}
	s.commits = append(s.commits, call{name: name, key: key, parent: info.Parent})
	return nil
}

func (s *recordingSnapshotter) Stat(_ context.Context, name string) (snapshots.Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.ContainsFunc(s.commits, func(c call) bool { return c.name == name }) {
		return snapshots.Info{Name: name}, nil
	}
	return snapshots.Info{}, errdefs.ErrNotFound
}

func (s *recordingSnapshotter) Remove(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removes = append(s.removes, key)
	return nil
}

// newTestUnpacker sets a 10-second deadline for unpack calls and test callbacks.
// The snapshotter declares the rebase capability.
func newTestUnpacker(t *testing.T, img *layerImage, sn snapshots.Snapshotter, a diff.Applier, opts ...UnpackerOpt) *Unpacker {
	t.Helper()
	return newTestUnpackerWithCaps(t, img, sn, a, []string{snapshots.RebaseCap}, opts...)
}

// newTestUnpackerWithCaps is newTestUnpacker with the snapshotter capabilities
// caps.
func newTestUnpackerWithCaps(t *testing.T, img *layerImage, sn snapshots.Snapshotter, a diff.Applier, caps []string, opts ...UnpackerOpt) *Unpacker {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	u, err := NewUnpacker(ctx, img.store.Store, append(opts, WithUnpackPlatform(Platform{
		SnapshotterKey:          "test",
		Snapshotter:             sn,
		Applier:                 a,
		SnapshotterCapabilities: caps,
	}))...)
	require.NoError(t, err)
	return u
}

// unpackMode returns the name and options of a sequential unpack when slots is
// 0, and of a parallel unpack with an unpack limiter of slots otherwise.
func unpackMode(slots int) (string, []UnpackerOpt) {
	if slots > 0 {
		return fmt.Sprintf("parallel/%d", slots), []UnpackerOpt{WithUnpackLimiter(semaphore.NewWeighted(int64(slots)))}
	}
	return "sequential", nil
}

// digestLog records the digests passed to a callback that runs concurrently.
type digestLog struct {
	mu      sync.Mutex
	digests []digest.Digest
}

func (l *digestLog) add(d digest.Digest) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.digests = append(l.digests, d)
}

// Sequential unpack prefetches cache hits above the first miss, then cancels
// and joins unfinished downloads after the layers commit. Independent
// preparation skips these prefetches.
func TestUnpackStagedAboveMiss(t *testing.T) {
	for _, slots := range []int{0, 1, 4} {
		parallel := slots > 0
		name, modeOpts := unpackMode(slots)
		t.Run(name, func(t *testing.T) {
			img := newLayerImage(t, "A", "B", "C", "D", "E")
			staged := map[digest.Digest]bool{}
			for _, i := range []int{0, 1, 3} {
				staged[img.layers[i].Digest] = true
			}
			sn := &recordingSnapshotter{staged: staged}
			stagedFetchStarted := make(chan struct{})
			stagedFetchStopped := make(chan struct{})

			var fetched, applied digestLog
			h := img.handler(func(ctx context.Context, desc ocispec.Descriptor) error {
				if parallel && staged[desc.Digest] {
					t.Errorf("staged layer %s fetched in parallel mode", desc.Digest)
				}
				fetched.add(desc.Digest)
				if desc.Digest == img.layers[3].Digest {
					close(stagedFetchStarted)
					<-ctx.Done()
					close(stagedFetchStopped)
					return ctx.Err()
				}
				return nil
			})
			a := img.applier(func(ctx context.Context, desc ocispec.Descriptor) error {
				if staged[desc.Digest] {
					t.Errorf("staged layer %s applied", desc.Digest)
				}
				applied.add(desc.Digest)
				if !parallel && desc.Digest == img.layers[2].Digest {
					return waitClosed(ctx, stagedFetchStarted)
				}
				return nil
			})
			u := newTestUnpacker(t, img, sn, a, modeOpts...)

			require.NoError(t, u.unpack(h, img.config, img.layers))

			want := []digest.Digest{img.layers[2].Digest, img.layers[4].Digest}
			assert.ElementsMatch(t, want, applied.digests)
			if !parallel {
				// The first layer that needs content requests every blob above it.
				want = append(want, img.layers[3].Digest)
				select {
				case <-stagedFetchStopped:
				default:
					t.Fatal("unpack returned before the speculative download stopped")
				}
				_, err := img.store.Store.Info(t.Context(), img.layers[3].Digest)
				assert.True(t, errdefs.IsNotFound(err), "unnecessary blob should not be downloaded: %v", err)
			}
			assert.ElementsMatch(t, want, fetched.digests)

			require.Len(t, sn.commits, len(img.layers))
			for i, c := range sn.commits {
				assert.Equal(t, img.chainIDs[i].String(), c.name)
				var wantParent string
				if parallel && i > 0 {
					wantParent = img.chainIDs[i-1].String()
				}
				assert.Equal(t, wantParent, c.parent)
			}
			assert.Empty(t, sn.removes)
		})
	}
}

// countingLimiter is a Limiter without a bound. It counts the slots held.
type countingLimiter struct{ held atomic.Int64 }

func (l *countingLimiter) Acquire(_ context.Context, n int64) error {
	l.held.Add(n)
	return nil
}

func (l *countingLimiter) Release(n int64) { l.held.Add(-n) }

func TestUnpackApplySlotNotHeldForDownload(t *testing.T) {
	// Without the rebase capability, an unpack with an unpack limiter is
	// sequential.
	for name, caps := range map[string][]string{
		"parallel":   {snapshots.RebaseCap},
		"sequential": nil,
	} {
		t.Run(name, func(t *testing.T) {
			img := newLayerImage(t, "A")
			sn := &recordingSnapshotter{}
			limiter := &countingLimiter{}
			h := img.handler(func(context.Context, ocispec.Descriptor) error {
				if n := limiter.held.Load(); n != 0 {
					t.Errorf("%d unpack slots held during download", n)
				}
				return nil
			})
			a := img.applier(func(context.Context, ocispec.Descriptor) error {
				if n := limiter.held.Load(); n != 1 {
					t.Errorf("%d unpack slots held during apply, want 1", n)
				}
				return nil
			})
			u := newTestUnpackerWithCaps(t, img, sn, a, caps, WithUnpackLimiter(limiter))

			require.NoError(t, u.unpack(h, img.config, img.layers))
			require.Len(t, sn.commits, 1)
			assert.Zero(t, limiter.held.Load())
		})
	}
}

func TestUnpackParallelApplyError(t *testing.T) {
	img := newLayerImage(t, "A", "B", "C")
	sn := &recordingSnapshotter{}
	errApply := errors.New("apply failed")
	// Layer 1 fails after layers 0 and 2 start applying.
	layer0Applying, layer2Applying := make(chan struct{}), make(chan struct{})
	a := img.applier(func(ctx context.Context, desc ocispec.Descriptor) error {
		switch desc.Digest {
		case img.layers[0].Digest:
			close(layer0Applying)
		case img.layers[1].Digest:
			for _, c := range []chan struct{}{layer0Applying, layer2Applying} {
				if err := waitClosed(ctx, c); err != nil {
					return err
				}
			}
			return errApply
		case img.layers[2].Digest:
			close(layer2Applying)
		}
		return nil
	})
	u := newTestUnpacker(t, img, sn, a, WithUnpackLimiter(semaphore.NewWeighted(4)))

	err := u.unpack(img.handler(nil), img.config, img.layers)
	require.ErrorIs(t, err, errApply)

	require.Len(t, sn.commits, 1)
	assert.Equal(t, img.chainIDs[0].String(), sn.commits[0].name)

	require.Len(t, sn.prepares, 3)
	assert.ElementsMatch(t, []string{sn.prepares[1].key, sn.prepares[2].key}, sn.removes)
}

// Layers below a failed extraction can finish applying and commit.
func TestUnpackParallelCommitsAppliedLayers(t *testing.T) {
	img := newLayerImage(t, "A", "B", "C", "D")
	sn := &recordingSnapshotter{}
	errApply := errors.New("apply failed")
	// Layer 2 fails while layers 0 and 1 are applying. The download of blob 3
	// returns only after the failure cancels it. Layer 1 finishes applying
	// after that, so it reaches Commit after the failure.
	layer0Applying, layer1Applying, canceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	blob3Started := make(chan struct{})
	h := img.handler(func(ctx context.Context, desc ocispec.Descriptor) error {
		if desc.Digest != img.layers[3].Digest {
			return nil
		}
		close(blob3Started)
		<-ctx.Done()
		close(canceled)
		return ctx.Err()
	})
	a := img.applier(func(ctx context.Context, desc ocispec.Descriptor) error {
		switch desc.Digest {
		case img.layers[0].Digest:
			close(layer0Applying)
		case img.layers[1].Digest:
			close(layer1Applying)
			return waitClosed(ctx, canceled)
		case img.layers[2].Digest:
			for _, c := range []chan struct{}{layer0Applying, layer1Applying, blob3Started} {
				if err := waitClosed(ctx, c); err != nil {
					return err
				}
			}
			return errApply
		}
		return nil
	})
	u := newTestUnpacker(t, img, sn, a, WithUnpackLimiter(semaphore.NewWeighted(4)))

	err := u.unpack(h, img.config, img.layers)
	require.ErrorIs(t, err, errApply)

	require.Len(t, sn.commits, 2)
	for i, c := range sn.commits {
		assert.Equal(t, img.chainIDs[i].String(), c.name)
	}
	require.Len(t, sn.prepares, 4)
	assert.ElementsMatch(t, []string{sn.prepares[2].key, sn.prepares[3].key}, sn.removes)
}

func TestUnpackParallelFailureKeepsLayersBelow(t *testing.T) {
	img := newLayerImage(t, "A", "B", "C")
	sn := &recordingSnapshotter{}
	errApply := errors.New("apply failed")
	// Layer 1 fails after the download of blob 2 starts. Blob 0 arrives after
	// the failure has canceled the download of blob 2.
	blob2Started, blob2Canceled := make(chan struct{}), make(chan struct{})
	h := img.handler(func(ctx context.Context, desc ocispec.Descriptor) error {
		switch desc.Digest {
		case img.layers[0].Digest:
			if err := waitClosed(ctx, blob2Canceled); err != nil {
				t.Errorf("download of blob 0 canceled: %v", err)
				return err
			}
		case img.layers[2].Digest:
			close(blob2Started)
			<-ctx.Done()
			close(blob2Canceled)
			return ctx.Err()
		}
		return nil
	})
	a := img.applier(func(ctx context.Context, desc ocispec.Descriptor) error {
		if desc.Digest != img.layers[1].Digest {
			return nil
		}
		if err := waitClosed(ctx, blob2Started); err != nil {
			return err
		}
		return errApply
	})
	u := newTestUnpacker(t, img, sn, a, WithUnpackLimiter(semaphore.NewWeighted(4)))

	err := u.unpack(h, img.config, img.layers)
	require.ErrorIs(t, err, errApply)
	require.Len(t, sn.commits, 1)
	assert.Equal(t, img.chainIDs[0].String(), sn.commits[0].name)
	require.Len(t, sn.prepares, 3)
	assert.ElementsMatch(t, []string{sn.prepares[1].key, sn.prepares[2].key}, sn.removes)
}

func TestUnpackParallelJoinsErrors(t *testing.T) {
	img := newLayerImage(t, "A", "B", "C")
	sn := &recordingSnapshotter{}
	errB, errC := errors.New("apply B failed"), errors.New("apply C failed")
	// Layer 2 waits for layer 1 to reach its failure before returning its own error.
	layer2Applying, layer1Failing := make(chan struct{}), make(chan struct{})
	a := img.applier(func(ctx context.Context, desc ocispec.Descriptor) error {
		switch desc.Digest {
		case img.layers[1].Digest:
			if err := waitClosed(ctx, layer2Applying); err != nil {
				return err
			}
			close(layer1Failing)
			return errB
		case img.layers[2].Digest:
			close(layer2Applying)
			if err := waitClosed(ctx, layer1Failing); err != nil {
				return err
			}
			return errC
		}
		return nil
	})
	u := newTestUnpacker(t, img, sn, a, WithUnpackLimiter(semaphore.NewWeighted(4)))

	err := u.unpack(img.handler(nil), img.config, img.layers)
	assert.ErrorIs(t, err, errB)
	assert.ErrorIs(t, err, errC)
	assert.NotErrorIs(t, err, context.Canceled)
}

// waitingLimiter is a Limiter backed by a semaphore. It counts the callers
// waiting in Acquire.
type waitingLimiter struct {
	sem     *semaphore.Weighted
	waiting atomic.Int64
}

func (l *waitingLimiter) Acquire(ctx context.Context, n int64) error {
	l.waiting.Add(1)
	defer l.waiting.Add(-1)
	return l.sem.Acquire(ctx, n)
}

func (l *waitingLimiter) Release(n int64) { l.sem.Release(n) }

// Each job may queue only one waiter on the shared unpack limiter.
func TestUnpackSharedLimiterTakesTurns(t *testing.T) {
	big := newLayerImage(t, "A0", "A1", "A2", "A3")
	small := newLayerImage(t, "B0")
	limiter := &waitingLimiter{sem: semaphore.NewWeighted(1)}

	var applied digestLog
	var first atomic.Bool
	bigApplying := make(chan struct{})
	bigApplier := big.applier(func(ctx context.Context, desc ocispec.Descriptor) error {
		applied.add(desc.Digest)
		if !first.CompareAndSwap(false, true) {
			return nil
		}
		close(bigApplying)
		// The first layer to apply holds the slot until two layers wait for
		// it. Only one of them can belong to the first image.
		for limiter.waiting.Load() < 2 {
			if err := ctx.Err(); err != nil {
				return err
			}
			time.Sleep(time.Millisecond)
		}
		return nil
	})
	smallApplier := small.applier(func(_ context.Context, desc ocispec.Descriptor) error {
		applied.add(desc.Digest)
		return nil
	})
	ub := newTestUnpacker(t, big, &recordingSnapshotter{}, bigApplier, WithUnpackLimiter(limiter))
	us := newTestUnpacker(t, small, &recordingSnapshotter{}, smallApplier, WithUnpackLimiter(limiter))

	errc := make(chan error, 1)
	go func() { errc <- ub.unpack(big.handler(nil), big.config, big.layers) }()
	// The second unpack starts after a layer of the first image holds the
	// slot, every blob of the first image is in the content store, and a
	// layer of the first image waits for the slot.
	require.NoError(t, waitClosed(ub.ctx, bigApplying))
	require.Eventually(t, func() bool {
		for _, desc := range big.layers {
			if _, err := big.store.Store.Info(ub.ctx, desc.Digest); err != nil {
				return false
			}
		}
		return limiter.waiting.Load() >= 1
	}, 10*time.Second, time.Millisecond)
	require.NoError(t, us.unpack(small.handler(nil), small.config, small.layers))
	require.NoError(t, <-errc)

	order := applied.digests
	require.Len(t, order, 5)
	assert.LessOrEqual(t, slices.Index(order, small.layers[0].Digest), 2, "apply order %v", order)
}

// A fetch-all download failure cancels other downloads and pending extractions.
// An extraction already in progress can finish and commit.
func TestUnpackFetchAllDownloadErrorStopsUnpack(t *testing.T) {
	img := newLayerImage(t, "A", "B", "C", "D")
	sn := &recordingSnapshotter{}
	errFetch := errors.New("fetch failed")
	// Blob 3 fails while layer 0 is applying and blob 1 is still downloading.
	layer0Applying, fetchFailed := make(chan struct{}), make(chan struct{})
	h := img.handler(func(ctx context.Context, desc ocispec.Descriptor) error {
		switch desc.Digest {
		case img.layers[1].Digest:
			// The download of blob 1 returns only after it is canceled.
			<-ctx.Done()
			return ctx.Err()
		case img.layers[3].Digest:
			if err := waitClosed(ctx, layer0Applying); err != nil {
				return err
			}
			close(fetchFailed)
			return errFetch
		}
		return nil
	})
	var applied digestLog
	a := img.applier(func(ctx context.Context, desc ocispec.Descriptor) error {
		applied.add(desc.Digest)
		close(layer0Applying)
		return waitClosed(ctx, fetchFailed)
	})
	u := newTestUnpacker(t, img, sn, a, WithFetchAllContent())

	err := u.unpack(h, img.config, img.layers)
	require.ErrorIs(t, err, errFetch)
	assert.Equal(t, []digest.Digest{img.layers[0].Digest}, applied.digests)
	require.Len(t, sn.commits, 1)
	assert.Equal(t, img.chainIDs[0].String(), sn.commits[0].name)
}

// A layer waiting for its parent to commit must release its apply slot.
// The parent may need that slot to finish extraction.
func TestUnpackParallelSlotNotHeldForCommit(t *testing.T) {
	img := newLayerImage(t, "A", "B")
	sn := &recordingSnapshotter{}
	// Blob 0 arrives after layer 1 has applied.
	layer1Applied := make(chan struct{})
	h := img.handler(func(ctx context.Context, desc ocispec.Descriptor) error {
		if desc.Digest != img.layers[0].Digest {
			return nil
		}
		return waitClosed(ctx, layer1Applied)
	})
	a := img.applier(func(_ context.Context, desc ocispec.Descriptor) error {
		if desc.Digest == img.layers[1].Digest {
			close(layer1Applied)
		}
		return nil
	})
	u := newTestUnpacker(t, img, sn, a, WithUnpackLimiter(semaphore.NewWeighted(1)))

	require.NoError(t, u.unpack(h, img.config, img.layers))
	require.Len(t, sn.commits, 2)
}

func TestUnpackDownloadAdmissionOrder(t *testing.T) {
	img := newLayerImage(t, "A", "B", "C", "D", "E", "F", "G", "H")
	sn := &recordingSnapshotter{}
	var fetched digestLog
	h := img.handler(func(_ context.Context, desc ocispec.Descriptor) error {
		fetched.add(desc.Digest)
		return nil
	})
	// The semaphore grants its one slot in Acquire order. Downloads therefore
	// run in the order they acquired the slot.
	u := newTestUnpacker(t, img, sn, img.applier(nil), WithLimiter(semaphore.NewWeighted(1)))

	require.NoError(t, u.unpack(h, img.config, img.layers))
	var want []digest.Digest
	for _, l := range img.layers {
		want = append(want, l.Digest)
	}
	assert.Equal(t, want, fetched.digests)
}

func TestUnpackSharedBlob(t *testing.T) {
	for _, mode := range []string{"sequential", "parallel", "fetch-all", "parallel-fetch-all", "fetch-only"} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fail=%t", mode, fail), func(t *testing.T) {
				img := newLayerImage(t, "A", "B", "A")
				occurrences := []string{"first", "middle", "repeated"}
				for i, occurrence := range occurrences {
					img.layers[i].Annotations = map[string]string{"occurrence": occurrence}
				}
				sn := &recordingSnapshotter{}
				seen := make(chan string, len(img.layers))
				errHandler := errors.New("repeated descriptor rejected")
				h := img.handler(func(_ context.Context, desc ocispec.Descriptor) error {
					occurrence := desc.Annotations["occurrence"]
					seen <- occurrence
					if fail && occurrence == "repeated" {
						return errHandler
					}
					return nil
				})
				opts := []UnpackerOpt{WithDuplicationSuppressor(kmutex.New())}
				if mode == "parallel" || mode == "parallel-fetch-all" {
					opts = append(opts, WithUnpackLimiter(semaphore.NewWeighted(4)))
				}
				if mode == "fetch-all" || mode == "parallel-fetch-all" {
					opts = append(opts, WithFetchAllContent())
				}
				u := newTestUnpacker(t, img, sn, img.applier(nil), opts...)
				var err error
				if mode == "fetch-only" {
					err = u.fetch(u.ctx, h, img.layers)
				} else {
					err = u.unpack(h, img.config, img.layers)
				}
				if fail {
					require.ErrorIs(t, err, errHandler)
					return
				}
				require.NoError(t, err)
				close(seen)
				var handled []string
				for occurrence := range seen {
					handled = append(handled, occurrence)
				}
				assert.ElementsMatch(t, occurrences, handled)
				if mode == "fetch-only" {
					assert.Empty(t, sn.prepares)
					return
				}
				require.Len(t, sn.commits, len(img.layers))
				for i, c := range sn.commits {
					assert.Equal(t, img.chainIDs[i].String(), c.name)
				}
			})
		}
	}
}

// Fetch-all must wait for a staged layer's blob and report its failure even
// when every snapshot has already committed.
func TestUnpackFetchAllStagedAboveMiss(t *testing.T) {
	for _, slots := range []int{0, 4} {
		for _, fail := range []bool{false, true} {
			name, opts := unpackMode(slots)
			t.Run(fmt.Sprintf("%s/fail=%t", name, fail), func(t *testing.T) {
				img := newLayerImage(t, "A", "B")
				sn := &commitNotifySnapshotter{
					recordingSnapshotter: recordingSnapshotter{staged: map[digest.Digest]bool{img.layers[1].Digest: true}},
					last:                 img.chainIDs[1].String(), done: make(chan struct{}),
				}
				errFetch := errors.New("staged blob fetch failed")
				h := img.handler(func(ctx context.Context, desc ocispec.Descriptor) error {
					if desc.Digest == img.layers[1].Digest {
						if err := waitClosed(ctx, sn.done); err != nil {
							return err
						}
						if fail {
							return errFetch
						}
					}
					return nil
				})
				u := newTestUnpacker(t, img, sn, img.applier(nil), append(opts, WithFetchAllContent())...)
				err := u.unpack(h, img.config, img.layers)
				if fail {
					require.ErrorIs(t, err, errFetch)
				} else {
					require.NoError(t, err)
					_, err := img.store.Store.Info(t.Context(), img.layers[1].Digest)
					require.NoError(t, err)
				}
				require.Len(t, sn.commits, 2)
				assert.Empty(t, sn.removes)
			})
		}
	}
}

func TestUnpackConcurrentSharedChain(t *testing.T) {
	img := newLayerImage(t, "A", "B", "C")
	sn := &recordingSnapshotter{}
	locker := kmutex.New()
	var applied digestLog
	a := img.applier(func(_ context.Context, desc ocispec.Descriptor) error {
		applied.add(desc.Digest)
		return nil
	})
	opts := []UnpackerOpt{WithDuplicationSuppressor(locker), WithUnpackLimiter(semaphore.NewWeighted(1))}
	first := newTestUnpacker(t, img, sn, a, opts...)
	second := newTestUnpacker(t, img, sn, a, opts...)
	errs := make(chan error, 2)
	for _, u := range []*Unpacker{first, second} {
		go func() { errs <- u.unpack(img.handler(nil), img.config, img.layers) }()
	}
	for range 2 {
		require.NoError(t, <-errs)
	}
	assert.Len(t, applied.digests, len(img.layers))
	assert.Len(t, sn.commits, len(img.layers))
	assert.Empty(t, sn.removes)
}

type commitErrorSnapshotter struct {
	recordingSnapshotter
	failName string
	err      error
}

func (s *commitErrorSnapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	if name == s.failName {
		return s.err
	}
	return s.recordingSnapshotter.Commit(ctx, name, key, opts...)
}

// A failed commit keeps lower snapshots, removes every uncommitted snapshot,
// and releases chain locks so another unpack can retry.
func TestUnpackCommitFailureCleanup(t *testing.T) {
	img := newLayerImage(t, "A", "B", "C")
	errCommit := errors.New("commit failed")
	sn := &commitErrorSnapshotter{failName: img.chainIDs[1].String(), err: errCommit}
	locker := kmutex.New()
	u := newTestUnpacker(t, img, sn, img.applier(nil),
		WithDuplicationSuppressor(locker), WithUnpackLimiter(semaphore.NewWeighted(2)))
	require.ErrorIs(t, u.unpack(img.handler(nil), img.config, img.layers), errCommit)
	require.Len(t, sn.prepares, 3)
	require.Len(t, sn.commits, 1)
	assert.Equal(t, img.chainIDs[0].String(), sn.commits[0].name)
	assert.ElementsMatch(t, []string{sn.prepares[1].key, sn.prepares[2].key}, sn.removes)
	sn.failName = ""
	require.NoError(t, u.unpack(img.handler(nil), img.config, img.layers))
	assert.Len(t, sn.commits, 3)
}

type retryPrepareSnapshotter struct {
	recordingSnapshotter
	calls int
}

func (s *retryPrepareSnapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	s.calls++
	if s.calls == 1 {
		return nil, errdefs.ErrAlreadyExists
	}
	return s.recordingSnapshotter.Prepare(ctx, key, parent, opts...)
}

func TestUnpackPrepareRetry(t *testing.T) {
	img := newLayerImage(t, "A")
	sn := &retryPrepareSnapshotter{}
	u := newTestUnpacker(t, img, sn, img.applier(nil), WithDuplicationSuppressor(kmutex.New()))
	require.NoError(t, u.unpack(img.handler(nil), img.config, img.layers))
	assert.Equal(t, 2, sn.calls)
	assert.Len(t, sn.commits, 1)
	assert.Empty(t, sn.removes)
}

func TestUnpackCallerCancellation(t *testing.T) {
	for _, duringApply := range []bool{false, true} {
		t.Run(fmt.Sprintf("duringApply=%t", duringApply), func(t *testing.T) {
			img := newLayerImage(t, "A")
			sn := &recordingSnapshotter{}
			errCanceled := errors.New("caller stopped unpack")
			var cancel context.CancelCauseFunc
			stop := func(ctx context.Context, _ ocispec.Descriptor) error {
				cancel(errCanceled)
				<-ctx.Done()
				return ctx.Err()
			}
			h, a := img.handler(nil), img.applier(nil)
			if duringApply {
				a = img.applier(stop)
			} else {
				h = img.handler(stop)
			}
			u := newTestUnpacker(t, img, sn, a, WithUnpackLimiter(semaphore.NewWeighted(1)))
			u.ctx, cancel = context.WithCancelCause(u.ctx)
			defer cancel(nil)
			require.ErrorIs(t, u.unpack(h, img.config, img.layers), errCanceled)
			require.Len(t, sn.prepares, 1)
			assert.Empty(t, sn.commits)
			assert.Equal(t, []string{sn.prepares[0].key}, sn.removes)
		})
	}
}

type updateErrorStore struct {
	content.Store
	digest digest.Digest
	err    error
}

func (s *updateErrorStore) Update(ctx context.Context, info content.Info, fields ...string) (content.Info, error) {
	if info.Digest == s.digest {
		return content.Info{}, s.err
	}
	return s.Store.Update(ctx, info, fields...)
}

func TestUnpackLabelFailureKeepsCommittedSnapshot(t *testing.T) {
	img := newLayerImage(t, "A")
	sn := &recordingSnapshotter{}
	errUpdate := errors.New("label update failed")
	u := newTestUnpacker(t, img, sn, img.applier(nil))
	u.content = &updateErrorStore{Store: u.content, digest: img.layers[0].Digest, err: errUpdate}
	require.ErrorIs(t, u.unpack(img.handler(nil), img.config, img.layers), errUpdate)
	require.Len(t, sn.commits, 1)
	assert.Equal(t, img.chainIDs[0].String(), sn.commits[0].name)
	assert.Empty(t, sn.removes)
}

// The second download starts after the speculative failure has completed.
func TestSpeculativeBlobFailureDoesNotCancelDownloads(t *testing.T) {
	img := newLayerImage(t, "bad", "good")
	errFetch := errors.New("speculative download failed")
	u := newTestUnpacker(t, img, &recordingSnapshotter{}, img.applier(nil))
	h := img.handler(func(_ context.Context, desc ocispec.Descriptor) error {
		if desc.Digest == img.layers[0].Digest {
			return errFetch
		}
		return nil
	})
	group := new(errgroup.Group)
	blobs := newBlobSet(u, h, group)
	ctx, cancel := context.WithCancel(u.ctx)
	defer func() {
		cancel()
		_ = group.Wait()
	}()
	failed := blobs.fetch(ctx, 0, img.layers[0])
	require.NoError(t, waitClosed(ctx, failed.done))
	require.ErrorIs(t, failed.err, errFetch)
	good := blobs.fetch(ctx, 1, img.layers[1])
	require.NoError(t, waitClosed(ctx, good.done))
	require.NoError(t, good.err)
	require.ErrorIs(t, group.Wait(), errFetch)
}

type commitNotifySnapshotter struct {
	recordingSnapshotter
	last string
	done chan struct{}
}

func (s *commitNotifySnapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	if err := s.recordingSnapshotter.Commit(ctx, name, key, opts...); err != nil {
		return err
	}
	if name == s.last {
		close(s.done)
	}
	return nil
}

// Default unpack must succeed when an unnecessary blob fails before a needed
// download finishes, during Apply, or after the last commit. Fetch-all must
// report that same error in every case.
func TestUnpackStagedDownloadFailureTiming(t *testing.T) {
	for _, fetchAll := range []bool{false, true} {
		for _, phase := range []string{"download", "apply", "committed"} {
			t.Run(fmt.Sprintf("fetchAll=%t/%s", fetchAll, phase), func(t *testing.T) {
				testCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				img := newLayerImage(t, "A", "cached", "B")
				sn := &commitNotifySnapshotter{
					recordingSnapshotter: recordingSnapshotter{staged: map[digest.Digest]bool{img.layers[1].Digest: true}},
					last:                 img.chainIDs[2].String(), done: make(chan struct{}),
				}
				errFetch := errors.New("cached blob download failed")
				failed, applying := make(chan struct{}), make(chan struct{})
				h := img.handler(func(ctx context.Context, desc ocispec.Descriptor) error {
					switch desc.Digest {
					case img.layers[0].Digest:
						if phase == "download" {
							return waitClosed(ctx, failed)
						}
					case img.layers[1].Digest:
						if phase == "apply" {
							if err := waitClosed(ctx, applying); err != nil {
								return err
							}
						} else if phase == "committed" {
							// This failure waits for the last commit.
							if err := waitClosed(testCtx, sn.done); err != nil {
								return err
							}
						}
						close(failed)
						return errFetch
					}
					return nil
				})
				a := img.applier(func(ctx context.Context, desc ocispec.Descriptor) error {
					if phase == "apply" && desc.Digest == img.layers[0].Digest {
						close(applying)
						return waitClosed(ctx, failed)
					}
					return nil
				})
				var opts []UnpackerOpt
				if fetchAll {
					opts = append(opts, WithFetchAllContent())
				}
				u := newTestUnpacker(t, img, sn, a, opts...)
				err := u.unpack(h, img.config, img.layers)
				if fetchAll {
					require.ErrorIs(t, err, errFetch)
				} else {
					require.NoError(t, err)
					assert.Len(t, sn.commits, len(img.layers))
					assert.Empty(t, sn.removes)
				}
				select {
				case <-failed:
				default:
					t.Fatal("the speculative fetch did not fail")
				}
			})
		}
	}
}

func TestUnpackPrefetchFailureReportedWhenNeeded(t *testing.T) {
	img := newLayerImage(t, "A", "B", "failed")
	sn := &recordingSnapshotter{}
	errFetch := errors.New("required blob download failed")
	failed := make(chan struct{})
	var applied digestLog
	h := img.handler(func(ctx context.Context, desc ocispec.Descriptor) error {
		if desc.Digest == img.layers[2].Digest {
			close(failed)
			return errFetch
		}
		return waitClosed(ctx, failed)
	})
	a := img.applier(func(_ context.Context, desc ocispec.Descriptor) error {
		applied.add(desc.Digest)
		return nil
	})
	u := newTestUnpacker(t, img, sn, a)
	require.ErrorIs(t, u.unpack(h, img.config, img.layers), errFetch)
	assert.Equal(t, []digest.Digest{img.layers[0].Digest, img.layers[1].Digest}, applied.digests)
	require.Len(t, sn.commits, 2)
	require.Len(t, sn.prepares, 3)
	assert.Equal(t, []string{sn.prepares[2].key}, sn.removes)
}
