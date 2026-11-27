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
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/internal/cleanup"
	"github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/containerd/v2/pkg/tracing"
)

// TODO: Follow-up work for the unpacker rewrite:
//   - Bound the number of goroutines waiting for download and apply slots.
//     Download requests must let preparation continue; extraction must wait for
//     its blob before acquiring an apply slot. Preserve ordered commits, allow
//     lower layers to finish after an upper-layer failure, and join workers on exit.
//   - Define how direct and proxy appliers advertise support for extraction
//     without a parent filesystem (#13053). Use that contract to replace the
//     overlay workaround and evaluate independent preparation without an unpack
//     limiter, which would extend granular EROFS cache hits to that configuration.
//   - Let a snapshotter declare whether it can serve a layer from a cache. In
//     sequential mode, a failed download of a blob above the first miss fails
//     the unpack only when that layer needs extraction, after the layers below
//     it are committed. Without a cache, every such blob is needed, and the
//     first failed download can fail the unpack right away.

// preparationMode determines whether extraction includes the parent filesystem.
// Independent preparation requires rebasing and an applier that preserves
// layer semantics without the parent.
type preparationMode int

const (
	prepareWithParent preparationMode = iota
	prepareIndependent
)

type layerDisposition int

const (
	// layerUnprepared covers failed or canceled preparation.
	layerUnprepared layerDisposition = iota
	layerExisting
	layerStaged
	layerNeedsApply
)

// unpackJob prepares and commits layers in order. Independent preparation allows
// concurrent extraction. Preparation with a parent waits for that parent's commit.
type unpackJob struct {
	u        *Unpacker
	p        *Platform
	diffIDs  []digest.Digest
	chainIDs []digest.Digest
	mode     preparationMode
}

// preparedLayer owns the extraction snapshot and chain ID lock until finish.
// When done is non-nil, the worker writes ready and err before closing it.
// Readers must wait for done to close.
type preparedLayer struct {
	job         *unpackJob
	desc        ocispec.Descriptor
	diffID      digest.Digest
	chainID     string
	disposition layerDisposition
	key         string
	mounts      []mount.Mount
	commitOpts  []snapshots.Opt
	unlock      func()
	span        *tracing.Span
	start       time.Time
	done        chan struct{}
	ready       bool
	err         error
}

func (j *unpackJob) run(opCtx context.Context, h images.Handler, layers []ocispec.Descriptor) error {
	ctx, cancel := context.WithCancelCause(opCtx)
	// A prefetched blob may be a cache hit. Its error matters only if a layer
	// needs extraction. Fetch-all requires every blob.
	downloads := new(errgroup.Group)
	if j.u.fetchAll {
		downloads, ctx = errgroup.WithContext(ctx)
	}
	blobs := newBlobSet(j.u, h, downloads)
	defer func() {
		cancel(nil)
		_ = downloads.Wait()
	}()
	if j.u.fetchAll {
		for i, desc := range layers {
			blobs.fetch(ctx, i, desc)
		}
	}

	// One waiter per job lets other jobs share the unpack limiter.
	queue := semaphore.NewWeighted(1)
	var pending []*preparedLayer
	var errs []error
	complete := true
	prefetched := j.u.fetchAll
	finish := func(l *preparedLayer) {
		if l.done != nil {
			<-l.done
		}
		complete = complete && l.ready
		if err := l.finish(opCtx, complete); err != nil {
			errs = append(errs, err)
			complete = false
		}
		if !complete {
			// Lower layers have finished. Running Apply calls use opCtx and
			// can finish after cancellation.
			cancel(nil)
		}
	}

	// In independent mode, each extraction inherits cancellation from the
	// layers below it. Its own failure cancels the layers above it.
	next := ctx
	for i, desc := range layers {
		if next.Err() != nil {
			// Prepared layers still need to be finished.
			break
		}
		l := j.prepare(next, opCtx, i, desc)
		if j.mode == prepareWithParent {
			if l.disposition == layerNeedsApply {
				b := blobs.fetch(ctx, i, desc)
				if !prefetched {
					// The first miss requests every blob above it.
					for above := i + 1; above < len(layers); above++ {
						blobs.fetch(ctx, above, layers[above])
					}
					prefetched = true
				}
				l.extract(ctx, opCtx, b, queue)
			}
			finish(l)
			if !complete {
				break
			}
			continue
		}

		if l.disposition == layerNeedsApply {
			workCtx, stop := context.WithCancelCause(next)
			defer stop(nil)
			next = workCtx
			b := blobs.fetch(workCtx, i, desc)
			l.done = make(chan struct{})
			go func() {
				defer close(l.done)
				l.extract(workCtx, opCtx, b, queue)
				if l.err != nil {
					stop(l.err)
				}
			}()
		}
		pending = append(pending, l)
		if l.disposition == layerUnprepared {
			break
		}
	}
	for _, l := range pending {
		finish(l)
	}
	if opCtx.Err() != nil {
		return context.Cause(opCtx)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	// A failed layer cancels ctx. A failed download in fetch-all mode cancels
	// it too, even after the last snapshot commits.
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if j.u.fetchAll {
		if err := downloads.Wait(); err != nil {
			return err
		}
	}
	// Deferred cleanup cancels and joins any unused prefetches.
	return nil
}

// prepare records whether the snapshot exists, has staged content, or needs
// extraction. The caller must finish the returned layer even on failure.
func (j *unpackJob) prepare(ctx, opCtx context.Context, i int, desc ocispec.Descriptor) *preparedLayer {
	_, span := tracing.StartSpan(opCtx, tracing.Name(unpackSpanPrefix, "unpackLayer"))
	setLayerAttributes(span, desc)
	l := &preparedLayer{
		job: j, desc: desc, diffID: j.diffIDs[i], chainID: j.chainIDs[i].String(),
		span: span, start: time.Now(),
	}
	var parentID string
	if i > 0 {
		parentID = j.chainIDs[i-1].String()
	}
	unlock, err := j.u.lockSnChainID(ctx, l.chainID, j.p.SnapshotterKey)
	if err != nil {
		if ctx.Err() == nil {
			l.err = err
		}
		return l
	}
	l.unlock = unlock
	snapshotLabels := layerSnapshotLabels(desc.Annotations)
	snapshotLabels[snapshots.LabelSnapshotRef] = l.chainID
	snapshotLabels[snapshots.LabelSnapshotDiffID] = l.diffID.String()
	if parentID != "" {
		snapshotLabels[labelSnapshotParent] = parentID
	}
	opts := append(slices.Clone(j.p.SnapshotOpts), snapshots.WithLabels(snapshotLabels))
	var parent string
	if j.mode == prepareWithParent {
		parent = parentID
	}
	for try := 1; try <= 3; try++ {
		key := fmt.Sprintf(snapshots.UnpackKeyFormat, uniquePart(), l.chainID)
		var mounts []mount.Mount
		mounts, err = j.p.Snapshotter.Prepare(opCtx, key, parent, opts...)
		if err == nil {
			l.key, l.mounts, l.commitOpts = key, mounts, opts
			l.disposition = layerNeedsApply
			if isStaged(mounts) {
				l.disposition, l.ready = layerStaged, true
			}
			if j.mode == prepareIndependent && parentID != "" {
				l.commitOpts = append(slices.Clip(opts), snapshots.WithParent(parentID))
				// Parentless overlay extraction needs whiteout conversion.
				// TODO: move this into the applier contract (#13053).
				if l.disposition == layerNeedsApply && j.p.SnapshotterKey == "overlayfs" {
					l.mounts = bindToOverlay(mounts)
				}
			}
			return l
		}
		if !errdefs.IsAlreadyExists(err) {
			l.err = fmt.Errorf("failed to prepare extraction snapshot %q: %w", key, err)
			return l
		}
		if info, err := j.p.Snapshotter.Stat(opCtx, l.chainID); err == nil {
			log.G(opCtx).Debugf("snapshot %s with chainID %s already exists, skip extraction of blob %q", info.Name, l.chainID, desc.Digest)
			l.disposition, l.ready = layerExisting, true
			l.unlock()
			l.unlock = nil
			return l
		} else if !errdefs.IsNotFound(err) {
			l.err = fmt.Errorf("failed to stat snapshot %s: %w", l.chainID, err)
			return l
		}
		log.G(opCtx).WithField("key", key).WithField("chainid", l.chainID).Debug("extraction snapshot already exists, chain id not found")
	}
	l.err = fmt.Errorf("unable to prepare extraction snapshot: %w", err)
	return l
}

// extract waits for content and an apply slot. Cancellation of a wait leaves
// ready false; an error from Apply is recorded even if another layer failed.
func (l *preparedLayer) extract(ctx, opCtx context.Context, b *blob, queue *semaphore.Weighted) {
	if waitClosed(ctx, b.done) != nil {
		return
	}
	if b.err != nil {
		l.err = b.err
		return
	}
	if queue.Acquire(ctx, 1) != nil {
		return
	}
	u := l.job.u
	err := u.acquire(ctx, u.unpackLimiter)
	queue.Release(1)
	if err != nil {
		if ctx.Err() == nil {
			l.err = err
		}
		return
	}
	defer u.release(u.unpackLimiter)
	if ctx.Err() != nil {
		return
	}
	diff, err := l.job.p.Applier.Apply(opCtx, l.desc, l.mounts, l.job.p.ApplyOpts...)
	if err != nil {
		l.err = fmt.Errorf("failed to extract layer (%s %s) to %s as %q: %w", l.desc.MediaType, l.desc.Digest, l.job.p.SnapshotterKey, l.key, err)
	} else if diff.Digest != l.diffID {
		l.err = fmt.Errorf("wrong diff id %q calculated on extraction %q, desc %q", diff.Digest, l.diffID, l.desc.Digest)
	} else {
		l.ready = true
	}
}

// finish commits or cleans up the layer and releases its lock.
// The coordinator calls it once, after any extraction worker returns.
func (l *preparedLayer) finish(ctx context.Context, commit bool) (retErr error) {
	sn := l.job.p.Snapshotter
	defer func() {
		if l.key != "" {
			cleanup.Do(ctx, func(ctx context.Context) {
				if err := sn.Remove(ctx, l.key); err != nil {
					log.G(ctx).WithError(err).Errorf("failed to cleanup %q", l.key)
				}
			})
		}
		if l.unlock != nil {
			l.unlock()
		}
		if retErr == nil && !commit {
			l.span.SetStatus(context.Canceled)
		} else {
			l.span.SetStatus(retErr)
		}
		l.span.End()
	}()
	if l.err != nil {
		return l.err
	}
	if !commit || l.disposition == layerExisting {
		return nil
	}
	if err := sn.Commit(ctx, l.chainID, l.key, l.commitOpts...); err != nil {
		if errdefs.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("failed to commit snapshot %s: %w", l.key, err)
	}
	l.key = ""
	if l.disposition == layerNeedsApply {
		// Apply has verified the diff ID for this content.
		info := content.Info{Digest: l.desc.Digest, Labels: map[string]string{labels.LabelUncompressed: l.diffID.String()}}
		if _, err := l.job.u.content.Update(ctx, info, "labels."+labels.LabelUncompressed); err != nil {
			return err
		}
	}
	log.G(ctx).WithFields(log.Fields{"layer": l.desc.Digest, "duration": time.Since(l.start)}).Debug("layer unpacked")
	return nil
}

func waitClosed(ctx context.Context, ch <-chan struct{}) error {
	select {
	case <-ch:
	case <-ctx.Done():
	}
	return context.Cause(ctx)
}
