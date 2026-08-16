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
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/internal/cleanup"
	"github.com/containerd/containerd/v2/internal/kmutex"
	"github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/containerd/v2/pkg/tracing"
)

const (
	labelSnapshotParent = "containerd.io/snapshot/parent-chain-id"
	unpackSpanPrefix    = "pkg.unpack.unpacker"
)

// Result returns information about the unpacks which were completed.
type Result struct {
	Unpacks int
}

type unpackerConfig struct {
	platforms []*Platform

	content content.Store

	limiter               Limiter
	duplicationSuppressor KeyedLocker
	unpackLimiter         Limiter
}

// Platform represents a platform-specific unpack configuration which includes
// the platform matcher as well as snapshotter and applier.
type Platform struct {
	Platform platforms.Matcher

	SnapshotterKey          string
	Snapshotter             snapshots.Snapshotter
	SnapshotOpts            []snapshots.Opt
	SnapshotterExports      map[string]string
	SnapshotterCapabilities []string

	Applier   diff.Applier
	ApplyOpts []diff.ApplyOpt

	// ConfigType is the supported config type to be considered for unpacking
	// Defaults to OCI image config
	ConfigType string

	// LayerTypes are the supported types to be considered layers
	// Defaults to OCI image layers
	LayerTypes []string
}

// KeyedLocker is an interface for managing job duplication by
// locking on a given key.
type KeyedLocker interface {
	Lock(ctx context.Context, key string) error
	Unlock(key string)
}

// Limiter interface is used to restrict the number of concurrent operations by
// requiring operations to first acquire from the limiter and release when complete.
type Limiter interface {
	Acquire(context.Context, int64) error
	Release(int64)
}

type UnpackerOpt func(*unpackerConfig) error

func WithUnpackPlatform(u Platform) UnpackerOpt {
	return UnpackerOpt(func(c *unpackerConfig) error {
		if u.Platform == nil {
			u.Platform = platforms.All
		}
		if u.Snapshotter == nil {
			return fmt.Errorf("snapshotter must be provided to unpack")
		}
		if u.SnapshotterKey == "" {
			if s, ok := u.Snapshotter.(fmt.Stringer); ok {
				u.SnapshotterKey = s.String()
			} else {
				u.SnapshotterKey = "unknown"
			}
		}
		if u.Applier == nil {
			return fmt.Errorf("applier must be provided to unpack")
		}

		c.platforms = append(c.platforms, &u)

		return nil
	})
}

func WithLimiter(l Limiter) UnpackerOpt {
	return UnpackerOpt(func(c *unpackerConfig) error {
		c.limiter = l
		return nil
	})
}

func WithDuplicationSuppressor(d KeyedLocker) UnpackerOpt {
	return UnpackerOpt(func(c *unpackerConfig) error {
		c.duplicationSuppressor = d
		return nil
	})
}

func WithUnpackLimiter(l Limiter) UnpackerOpt {
	return UnpackerOpt(func(c *unpackerConfig) error {
		c.unpackLimiter = l
		return nil
	})
}

// Unpacker unpacks images by hooking into the image handler process.
// Unpacks happen in the backgrounds and waited on to complete.
type Unpacker struct {
	unpackerConfig

	unpacks atomic.Int32
	ctx     context.Context
	eg      *errgroup.Group
}

// NewUnpacker creates a new instance of the unpacker which can be used to wrap an
// image handler and unpack in parallel to handling. The unpacker will handle
// calling the block handlers when they are needed by the unpack process.
func NewUnpacker(ctx context.Context, cs content.Store, opts ...UnpackerOpt) (*Unpacker, error) {
	eg, ctx := errgroup.WithContext(ctx)

	u := &Unpacker{
		unpackerConfig: unpackerConfig{
			content:               cs,
			duplicationSuppressor: kmutex.NewNoop(),
		},
		ctx: ctx,
		eg:  eg,
	}
	for _, opt := range opts {
		if err := opt(&u.unpackerConfig); err != nil {
			return nil, err
		}
	}
	if len(u.platforms) == 0 {
		return nil, fmt.Errorf("no unpack platforms defined: %w", errdefs.ErrInvalidArgument)
	}
	return u, nil
}

// Unpack wraps an image handler to filter out blob handling and scheduling them
// during the unpack process. When an image config is encountered, the unpack
// process will be started in a goroutine.
func (u *Unpacker) Unpack(h images.Handler) images.Handler {
	var (
		lock   sync.Mutex
		layers = map[digest.Digest][]ocispec.Descriptor{}
	)

	var layerTypes map[string]bool
	var configTypes map[string]bool
	for _, p := range u.platforms {
		if p.ConfigType != "" {
			if configTypes == nil {
				configTypes = make(map[string]bool)
			}
			configTypes[p.ConfigType] = true
		}
		if len(p.LayerTypes) > 0 {
			if layerTypes == nil {
				layerTypes = make(map[string]bool)
			}
			for _, t := range p.LayerTypes {
				layerTypes[t] = true
			}
		}
	}

	return images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		ctx, span := tracing.StartSpan(ctx, tracing.Name(unpackSpanPrefix, "UnpackHandler"))
		defer span.End()
		span.SetAttributes(
			tracing.Attribute("descriptor.media.type", desc.MediaType),
			tracing.Attribute("descriptor.digest", desc.Digest.String()))
		unlock, err := u.lockBlobDescriptor(ctx, desc)
		if err != nil {
			return nil, err
		}
		children, err := h.Handle(ctx, desc)
		unlock()
		if err != nil {
			return children, err
		}

		if images.IsManifestType(desc.MediaType) {
			var nonLayers []ocispec.Descriptor
			var manifestLayers []ocispec.Descriptor
			// Split layers from non-layers, layers will be handled after
			// the config
			for i, child := range children {
				span.SetAttributes(
					tracing.Attribute("descriptor.child."+strconv.Itoa(i), []string{child.MediaType, child.Digest.String()}),
				)
				if images.IsLayerType(child.MediaType) || layerTypes[child.MediaType] {
					manifestLayers = append(manifestLayers, child)
				} else {
					nonLayers = append(nonLayers, child)
				}
			}

			lock.Lock()
			for _, nl := range nonLayers {
				layers[nl.Digest] = manifestLayers
			}
			lock.Unlock()

			children = nonLayers
		} else if images.IsConfigType(desc.MediaType) || configTypes[desc.MediaType] {
			lock.Lock()
			l := layers[desc.Digest]
			lock.Unlock()
			if len(l) > 0 {
				u.eg.Go(func() error {
					return u.unpack(h, desc, l)
				})
			}
		}
		return children, nil
	})
}

// Wait waits for any ongoing unpack processes to complete then will return
// the result.
func (u *Unpacker) Wait() (Result, error) {
	if err := u.eg.Wait(); err != nil {
		return Result{}, err
	}
	return Result{
		Unpacks: int(u.unpacks.Load()),
	}, nil
}

// unpackConfig is a subset of the OCI config for resolving rootfs and platform,
// any config type which supports the platform and rootfs field can be supported.
type unpackConfig struct {
	// Platform describes the platform which the image in the manifest runs on.
	ocispec.Platform

	// RootFS references the layer content addresses used by the image.
	RootFS ocispec.RootFS `json:"rootfs"`
}

type unpackStatus struct {
	err     error
	desc    ocispec.Descriptor
	bottomF func(bool) error
	span    *tracing.Span
	startAt time.Time
}

// parentChainIDsForLayers returns, for each layer, the ChainID string of the
// nearest preceding layer which is not skippable (see
// images.IsSkippableLayerType), or "" if there is none. A skippable layer
// (currently only a standalone EROFS chunk-index layer) contributes no
// content and gets no snapshot of its own, so the next real layer's
// snapshot must be parented on the last real layer's snapshot instead of
// the immediately preceding one. chainIDs must have one entry per layer,
// pre-computed over every layer's DiffID regardless of skip status (e.g.
// via identity.ChainIDs), so that the final ChainID and every committed
// snapshot's ChainID are exactly as if every layer had contributed a
// snapshot.
func parentChainIDsForLayers(layers []ocispec.Descriptor, chainIDs []digest.Digest) []string {
	parentChainIDs := make([]string, len(layers))
	var lastChainID string
	for i, l := range layers {
		parentChainIDs[i] = lastChainID
		if !images.IsSkippableLayerType(l.MediaType) {
			lastChainID = chainIDs[i].String()
		}
	}
	return parentChainIDs
}

func (u *Unpacker) unpack(
	h images.Handler,
	config ocispec.Descriptor,
	layers []ocispec.Descriptor,
) error {
	ctx := u.ctx
	ctx, layerSpan := tracing.StartSpan(ctx, tracing.Name(unpackSpanPrefix, "unpack"))
	defer layerSpan.End()
	unpackStart := time.Now()
	p, err := content.ReadBlob(ctx, u.content, config)
	if err != nil {
		return err
	}

	var i unpackConfig
	if err := json.Unmarshal(p, &i); err != nil {
		return fmt.Errorf("unmarshal image config: %w", err)
	}

	// LayerIDs resolves each layer's identifier - a DiffID for a classic
	// layer, or an EROFS-image-layer-format-spec ID for one that carries
	// the AnnotationErofsUncompressedDigest annotation - falling back to
	// the layer's own blob digest when neither an annotation nor a
	// rootfs.diff_ids entry is present. See images.LayerIDs for the
	// verification invariant this relies on: the value used here to key a
	// snapshot's ChainID is always checked against the differ's own
	// digest of the applied content below.
	layerIDs, err := images.LayerIDs(layers, i.RootFS.DiffIDs)
	if err != nil {
		return err
	}

	// TODO: Support multiple unpacks rather than just first match
	var unpack *Platform

	imgPlatform := platforms.Normalize(i.Platform)
	for _, up := range u.platforms {
		if up.ConfigType != "" && up.ConfigType != config.MediaType {
			continue
		}
		// "layers" is only supported rootfs value for OCI images
		if (up.ConfigType == "" || images.IsConfigType(up.ConfigType)) && i.RootFS.Type != "" && i.RootFS.Type != "layers" {
			continue
		}
		if up.Platform.Match(imgPlatform) {
			unpack = up
			break
		}
	}

	if unpack == nil {
		log.G(ctx).WithField("image", config.Digest).WithField("platform", platforms.Format(imgPlatform)).Debugf("unpacker does not support platform, only fetching layers")
		return u.fetch(ctx, h, layers, nil)
	}

	u.unpacks.Add(1)

	var (
		sn = unpack.Snapshotter
		a  = unpack.Applier
		cs = u.content

		fetchOffset int
		fetchC      []chan struct{}
		fetchErr    []chan error

		parallel = u.supportParallel(unpack)
	)

	// If there is an early return, ensure any ongoing
	// fetches get their context cancelled
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// pre-calculate chain ids for each layer
	chainIDs := make([]digest.Digest, len(layerIDs))
	copy(chainIDs, layerIDs)
	chainIDs = identity.ChainIDs(chainIDs)

	parentChainIDs := parentChainIDsForLayers(layers, chainIDs)

	topHalf := func(i int, desc ocispec.Descriptor, span *tracing.Span, startAt time.Time) (<-chan *unpackStatus, error) {
		var (
			err     error
			parent  string
			chainID string
		)
		parentChainID := parentChainIDs[i]
		if parentChainID != "" && !parallel {
			parent = parentChainID
		}
		chainID = chainIDs[i].String()

		unlock, err := u.lockSnChainID(ctx, chainID, unpack.SnapshotterKey)
		if err != nil {
			return nil, err
		}
		defer func() {
			if err != nil {
				unlock()
			}
		}()

		// inherits annotations which are provided as snapshot labels.
		snapshotLabels := snapshots.FilterInheritedLabels(desc.Annotations)
		if snapshotLabels == nil {
			snapshotLabels = make(map[string]string)
		}
		snapshotLabels[snapshots.LabelSnapshotRef] = chainID
		snapshotLabels[snapshots.LabelSnapshotDiffID] = layerIDs[i].String()
		if parentChainID != "" {
			snapshotLabels[labelSnapshotParent] = parentChainID
		}

		var (
			key    string
			mounts []mount.Mount
			// Clone before appending: topHalf runs concurrently per layer in
			// parallel mode, and appending directly to unpack.SnapshotOpts could
			// write into its shared backing array from multiple goroutines.
			opts   = append(slices.Clone(unpack.SnapshotOpts), snapshots.WithLabels(snapshotLabels))
			staged bool
		)

		for try := 1; try <= 3; try++ {
			// Prepare snapshot with from parent, label as root
			key = fmt.Sprintf(snapshots.UnpackKeyFormat, uniquePart(), chainID)
			mounts, err = sn.Prepare(ctx, key, parent, opts...)
			if err != nil {
				if errdefs.IsAlreadyExists(err) {
					if snInfo, err := sn.Stat(ctx, chainID); err != nil {
						if !errdefs.IsNotFound(err) {
							return nil, fmt.Errorf("failed to stat snapshot %s: %w", chainID, err)
						}
						// Try again, this should be rare, log it
						log.G(ctx).WithField("key", key).WithField("chainid", chainID).Debug("extraction snapshot already exists, chain id not found")
					} else {
						log.G(ctx).Debugf("snapshot %s with chainID %s already exists skip fetch blob %q ", snInfo.Name, chainID, desc.Digest)
						// no need to handle, snapshot now found with chain id
						return nil, nil
					}
				} else {
					return nil, fmt.Errorf("failed to prepare extraction snapshot %q: %w", key, err)
				}
			} else {
				break
			}
		}
		if err != nil {
			return nil, fmt.Errorf("unable to prepare extraction snapshot: %w", err)
		}

		if isStaged(mounts) {
			// The snapshotter staged the layer content into the active snapshot
			// as read-only (e.g. a layer content cache hit). Skip fetch+apply,
			// but still commit it below (which applies the parent).
			staged = true
		}

		// Abort the snapshot if commit does not happen
		abort := func(ctx context.Context) {
			if err := sn.Remove(ctx, key); err != nil {
				log.G(ctx).WithError(err).Errorf("failed to cleanup %q", key)
			}
		}

		// commitF is the bottom half shared by normal and staged layers: it rebases
		// in the real parent (parallel mode) and commits the snapshot. Staged layers
		// have no fetched content, so they skip the post-apply uncompressed label.
		commitF := func(shouldAbort bool) error {
			defer unlock()
			if shouldAbort {
				cleanup.Do(ctx, abort)
				return nil
			}

			if parallel && parentChainID != "" {
				opts = append(opts, snapshots.WithParent(parentChainID))
			}
			if err := sn.Commit(ctx, chainID, key, opts...); err != nil {
				cleanup.Do(ctx, abort)
				if errdefs.IsAlreadyExists(err) {
					return nil
				}
				return fmt.Errorf("failed to commit snapshot %s: %w", key, err)
			}

			if staged {
				// No layer was fetched, so there is no content to label.
				return nil
			}

			// Set the uncompressed label after the uncompressed
			// digest has been verified through apply.
			cinfo := content.Info{
				Digest: desc.Digest,
				Labels: map[string]string{
					labels.LabelUncompressed: layerIDs[i].String(),
				},
			}
			if _, err := cs.Update(ctx, cinfo, "labels."+labels.LabelUncompressed); err != nil {
				return err
			}
			return nil
		}

		if staged {
			// Content is already staged in the active snapshot; there is nothing to
			// fetch or apply. Emit a status that runs commitF in the (serialized)
			// bottom half so the parent is rebased in and the chain is linked.
			resCh := make(chan *unpackStatus, 1)
			resCh <- &unpackStatus{
				desc:    desc,
				span:    span,
				startAt: startAt,
				bottomF: commitF,
			}
			close(resCh)
			return resCh, nil
		}

		if fetchErr == nil {
			fetchOffset = i
			n := len(layers) - fetchOffset
			fetchErr = make([]chan error, n)
			fetchC = make([]chan struct{}, n)
			for i := range n {
				fetchC[i] = make(chan struct{})
				fetchErr[i] = make(chan error, 1)
			}
			go func(i int) {
				err := u.fetch(ctx, h, layers[i:], fetchC)
				if err != nil {
					for _, fc := range fetchErr {
						fc <- err
						close(fc)
					}
				}
			}(i)
		}

		if err = u.acquire(ctx, u.unpackLimiter); err != nil {
			cleanup.Do(ctx, abort)
			return nil, err
		}

		resCh := make(chan *unpackStatus, 1)
		go func() {
			defer func() {
				u.release(u.unpackLimiter)
				close(resCh)
			}()

			status := &unpackStatus{
				desc:    desc,
				span:    span,
				startAt: startAt,
				bottomF: commitF,
			}

			select {
			case <-ctx.Done():
				cleanup.Do(ctx, abort)
				status.err = ctx.Err()
				resCh <- status
				return
			case err := <-fetchErr[i-fetchOffset]:
				if err != nil {
					cleanup.Do(ctx, abort)
					status.err = err
					resCh <- status
					return
				}
			case <-fetchC[i-fetchOffset]:
			}

			// In case of parallel unpack, the parent snapshot isn't provided to the snapshotter.
			// The overlayfs will return bind mounts for all layers, we need to convert them
			// to overlay mounts for the applier to perform whiteout conversion correctly.
			// TODO: this is a temporary workaround until #13053 lands.
			// See: https://github.com/containerd/containerd/issues/13030
			if parentChainID != "" && parallel && unpack.SnapshotterKey == "overlayfs" {
				mounts = bindToOverlay(mounts)
			}

			diff, err := a.Apply(ctx, desc, mounts, unpack.ApplyOpts...)
			if err != nil {
				cleanup.Do(ctx, abort)
				status.err = fmt.Errorf("failed to extract layer (%s %s) to %s as %q: %w", desc.MediaType, desc.Digest, unpack.SnapshotterKey, key, err)
				resCh <- status
				return
			}

			if diff.Digest != layerIDs[i] {
				// This is the verification the security invariant
				// documented on images.LayerIDs depends on: whatever
				// value LayerIDs resolved for this layer - an
				// annotation, a rootfs.diff_ids entry, or a blob-digest
				// fallback - must agree with what the differ actually
				// computed over the applied content, or the mismatch is
				// rejected here before any snapshot is keyed on it.
				cleanup.Do(ctx, abort)
				status.err = fmt.Errorf("wrong layer id %q calculated on extraction %q, desc %q", diff.Digest, layerIDs[i], desc.Digest)
				resCh <- status
				return
			}

			resCh <- status
		}()

		return resCh, nil
	}

	bottomHalf := func(s *unpackStatus, prevErrs error) error {
		var err error
		if s.err != nil {
			s.bottomF(true)
			err = s.err
		} else if prevErrs != nil {
			s.bottomF(true)
			err = fmt.Errorf("aborted")
		} else {
			err = s.bottomF(false)
		}

		s.span.SetStatus(err)
		s.span.End()
		if err == nil {
			log.G(ctx).WithFields(log.Fields{
				"layer":    s.desc.Digest,
				"duration": time.Since(s.startAt),
			}).Debug("layer unpacked")
		}
		return err
	}

	var (
		statusChans []<-chan *unpackStatus
		topErr      error
	)

	for i, desc := range layers {
		if images.IsSkippableLayerType(desc.MediaType) {
			// This layer contributes no content and gets no snapshot (see
			// parentChainIDs above), but its blob must still be persisted -
			// for a future consumer that does understand it (e.g. a chunk
			// store), and so that this image remains fully pushable/
			// exportable. Layers at or after the first non-skippable layer
			// are already covered by that layer's background fetch of
			// layers[i:] (fetchErr is set once that begins); only a
			// skippable layer preceding any real layer needs fetching here
			// directly.
			if fetchErr == nil {
				if err := u.fetch(ctx, h, layers[i:i+1], nil); err != nil {
					return err
				}
			}
			continue
		}

		_, layerSpan := tracing.StartSpan(ctx, tracing.Name(unpackSpanPrefix, "unpackLayer"))
		unpackLayerStart := time.Now()
		layerSpan.SetAttributes(
			tracing.Attribute("layer.media.type", desc.MediaType),
			tracing.Attribute("layer.media.size", desc.Size),
			tracing.Attribute("layer.media.digest", desc.Digest.String()),
		)
		statusCh, err := topHalf(i, desc, layerSpan, unpackLayerStart)
		if err != nil {
			layerSpan.SetStatus(err)
			layerSpan.End()
			if !parallel {
				return err
			}
			// Layers queued before the failure still need to be drained and
			// committed (or aborted) below, so remember the error and join it
			// after the drain instead of returning right away.
			topErr = err
			break
		}
		if statusCh == nil {
			// nothing to do, already exists
			layerSpan.End()
			continue
		}
		if parallel {
			statusChans = append(statusChans, statusCh)
		} else {
			if err = bottomHalf(<-statusCh, nil); err != nil {
				return err
			}
		}
	}

	// In parallel mode, snapshots still need to be committed and rebased sequentially
	if parallel {
		var errs error
		for _, sc := range statusChans {
			if err := bottomHalf(<-sc, errs); err != nil {
				errs = errors.Join(errs, err)
			}
		}
		errs = errors.Join(errs, topErr)
		if errs != nil {
			return errs
		}
	}

	var chainID string
	if len(chainIDs) > 0 {
		chainID = chainIDs[len(chainIDs)-1].String()
	}
	cinfo := content.Info{
		Digest: config.Digest,
		Labels: map[string]string{
			fmt.Sprintf("containerd.io/gc.ref.snapshot.%s", unpack.SnapshotterKey): chainID,
		},
	}
	_, err = cs.Update(ctx, cinfo, fmt.Sprintf("labels.containerd.io/gc.ref.snapshot.%s", unpack.SnapshotterKey))
	if err != nil {
		return err
	}
	log.G(ctx).WithFields(log.Fields{
		"config":   config.Digest,
		"chainID":  chainID,
		"parallel": parallel,
		"duration": time.Since(unpackStart),
	}).Debug("image unpacked")

	return nil
}

func (u *Unpacker) fetch(ctx context.Context, h images.Handler, layers []ocispec.Descriptor, done []chan struct{}) error {
	eg, ctx2 := errgroup.WithContext(ctx)
	for i, desc := range layers {
		ctx2, layerSpan := tracing.StartSpan(ctx2, tracing.Name(unpackSpanPrefix, "fetchLayer"))
		layerSpan.SetAttributes(
			tracing.Attribute("layer.media.type", desc.MediaType),
			tracing.Attribute("layer.media.size", desc.Size),
			tracing.Attribute("layer.media.digest", desc.Digest.String()),
		)
		var ch chan struct{}
		if done != nil {
			ch = done[i]
		}

		if err := u.acquire(ctx, u.limiter); err != nil {
			return err
		}

		eg.Go(func() error {
			defer layerSpan.End()

			unlock, err := u.lockBlobDescriptor(ctx2, desc)
			if err != nil {
				u.release(u.limiter)
				return err
			}

			_, err = h.Handle(ctx2, desc)

			unlock()
			u.release(u.limiter)

			if err != nil && !errors.Is(err, images.ErrSkipDesc) {
				return err
			}
			if ch != nil {
				close(ch)
			}

			return nil
		})
	}

	return eg.Wait()
}

func (u *Unpacker) acquire(ctx context.Context, l Limiter) error {
	if l == nil {
		return nil
	}
	return l.Acquire(ctx, 1)
}

func (u *Unpacker) release(l Limiter) {
	if l == nil {
		return
	}
	l.Release(1)
}

func (u *Unpacker) lockSnChainID(ctx context.Context, chainID, snapshotter string) (func(), error) {
	key := u.makeChainIDKeyWithSnapshotter(chainID, snapshotter)

	if err := u.duplicationSuppressor.Lock(ctx, key); err != nil {
		return nil, err
	}
	return func() {
		u.duplicationSuppressor.Unlock(key)
	}, nil
}

func (u *Unpacker) lockBlobDescriptor(ctx context.Context, desc ocispec.Descriptor) (func(), error) {
	key := u.makeBlobDescriptorKey(desc)

	if err := u.duplicationSuppressor.Lock(ctx, key); err != nil {
		return nil, err
	}
	return func() {
		u.duplicationSuppressor.Unlock(key)
	}, nil
}

func (u *Unpacker) makeChainIDKeyWithSnapshotter(chainID, snapshotter string) string {
	return fmt.Sprintf("sn://%s/%v", snapshotter, chainID)
}

func (u *Unpacker) makeBlobDescriptorKey(desc ocispec.Descriptor) string {
	return fmt.Sprintf("blob://%v", desc.Digest)
}

func (u *Unpacker) supportParallel(unpack *Platform) bool {
	if u.unpackLimiter == nil {
		return false
	}
	if !slices.Contains(unpack.SnapshotterCapabilities, snapshots.RebaseCap) {
		log.L.Infof("snapshotter does not support rebase capability, unpacking will be sequential")
		return false
	}
	return true
}

func uniquePart() string {
	t := time.Now()
	var b [3]byte
	// Ignore read failures, just decreases uniqueness
	rand.Read(b[:])
	return fmt.Sprintf("%d-%s", t.Nanosecond(), base64.URLEncoding.EncodeToString(b[:]))
}

// isStaged reports whether a successful Prepare has already staged the
// layer's content into the active snapshot instead of returning a normal,
// writable active snapshot (e.g. a snapshotter serving the layer from a local
// content cache). There is nothing to write into a staged snapshot, so the
// caller should skip fetching and applying the layer, and just Commit the
// snapshot as-is (applying the real parent at Commit time).
//
// Only the last mount in the slice is inspected: earlier entries are inputs
// consumed by mount templating (e.g. "{{ mount 0 }}" in an overlay's
// lowerdir) rather than the mount that is actually stacked on top, so they
// carry no information about writability.
func isStaged(mounts []mount.Mount) bool {
	if len(mounts) == 0 {
		return false
	}
	return mounts[len(mounts)-1].ReadOnly()
}

// TODO: this is a temporary workaround until #13053 lands.
func bindToOverlay(mounts []mount.Mount) []mount.Mount {
	if len(mounts) != 1 || mounts[0].Type != "bind" {
		return mounts
	}

	m := mount.Mount{
		Type:   "overlay",
		Source: "overlay",
	}
	for _, o := range mounts[0].Options {
		if o != "rbind" {
			m.Options = append(m.Options, o)
		}
	}
	m.Options = append(m.Options, "upperdir="+mounts[0].Source)

	return []mount.Mount{m}
}
