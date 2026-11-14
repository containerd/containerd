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
	"github.com/containerd/containerd/v2/internal/kmutex"
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

	content  content.Store
	fetchAll bool

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
			return errors.New("snapshotter must be provided to unpack")
		}
		if u.SnapshotterKey == "" {
			if s, ok := u.Snapshotter.(fmt.Stringer); ok {
				u.SnapshotterKey = s.String()
			} else {
				u.SnapshotterKey = "unknown"
			}
		}
		if u.Applier == nil {
			return errors.New("applier must be provided to unpack")
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

// WithFetchAllContent fetches all layers supplied to the unpacker, including
// layers whose snapshots already exist.
// It does not change the caller's platform selection.
func WithFetchAllContent() UnpackerOpt {
	return func(c *unpackerConfig) error {
		c.fetchAll = true
		return nil
	}
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
		lock sync.Mutex
		// Maps a config's digest to the layer descriptors of each manifest
		// that names it, one slice per manifest. Layers cannot be unpacked
		// until the diffIDs are known from the config. Manifests can share a
		// config (compression variants of one image have identical diffIDs
		// and so identical configs), which is why one digest can hold the
		// layers of several manifests.
		queuedLayers = map[digest.Digest][][]ocispec.Descriptor{}
		// Maps a config's digest to the layer descriptors selected for unpack.
		// Once these layers are queued for fetch, the value is set to nil. The key
		// remains to prevent scheduling another unpack.
		unpackedLayers = map[digest.Digest][]ocispec.Descriptor{}
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

			if len(manifestLayers) == 0 {
				return nonLayers, nil
			}

			lock.Lock()
			for _, nl := range nonLayers {
				if images.IsConfigType(nl.MediaType) || configTypes[nl.MediaType] {
					queuedLayers[nl.Digest] = append(queuedLayers[nl.Digest], manifestLayers)
				}
			}
			lock.Unlock()

			children = nonLayers
		} else if images.IsConfigType(desc.MediaType) || configTypes[desc.MediaType] {
			lock.Lock()
			queued := queuedLayers[desc.Digest]
			delete(queuedLayers, desc.Digest)
			// Because manifests that share the same config unpack to the same
			// snapshot chain, we only need to unpack the layers for one
			// manifest. The layers for any remaining manifests sharing that
			// config are still fetched to ensure they make it in the content
			// store.
			var first []ocispec.Descriptor
			unpacked, ok := unpackedLayers[desc.Digest]
			if !ok && len(queued) > 0 {
				first, queued = queued[0], queued[1:]
				unpacked = first
				unpackedLayers[desc.Digest] = first
			}
			// Unpack may skip fetching layers whose snapshots already exist.
			// If another manifest shares this config, explicitly fetch the first
			// manifest's layers too so every manifest's blobs reach the content
			// store. For a config used by only one manifest, leave fetching to
			// unpack.
			if len(queued) > 0 && len(unpacked) > 0 {
				queued = append(queued, unpacked)
				unpackedLayers[desc.Digest] = nil
			}
			lock.Unlock()
			if len(first) > 0 {
				u.eg.Go(func() error {
					return u.unpack(h, desc, first)
				})
			}
			for _, layers := range queued {
				u.eg.Go(func() error {
					return u.fetch(u.ctx, h, layers)
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

// layerSnapshotLabels filters out containerd.io/snapshot/uidmapping and
// .../gidmapping annotations from the (untrusted) image manifest to prevent
// snapshotters from incorrectly chowning the extracted layer to the supplied
// mapped host uid/gid.
func layerSnapshotLabels(annotations map[string]string) map[string]string {
	labels := snapshots.FilterInheritedLabels(annotations)
	if labels == nil {
		labels = make(map[string]string)
	}
	delete(labels, snapshots.LabelSnapshotUIDMapping)
	delete(labels, snapshots.LabelSnapshotGIDMapping)
	return labels
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

	diffIDs := i.RootFS.DiffIDs
	if len(layers) != len(diffIDs) {
		return fmt.Errorf("number of layers and diffIDs don't match: %d != %d", len(layers), len(diffIDs))
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
		return u.fetch(ctx, h, layers)
	}

	u.unpacks.Add(1)

	// pre-calculate chain ids for each layer
	chainIDs := make([]digest.Digest, len(diffIDs))
	copy(chainIDs, diffIDs)
	chainIDs = identity.ChainIDs(chainIDs)

	job := &unpackJob{
		u:        u,
		p:        unpack,
		diffIDs:  diffIDs,
		chainIDs: chainIDs,
		mode:     u.preparationMode(unpack),
	}
	if err := job.run(ctx, h, layers); err != nil {
		return err
	}

	var chainID string
	if len(chainIDs) > 0 {
		chainID = chainIDs[len(chainIDs)-1].String()
	}
	cinfo := content.Info{
		Digest: config.Digest,
		Labels: map[string]string{
			"containerd.io/gc.ref.snapshot." + unpack.SnapshotterKey: chainID,
		},
	}
	_, err = u.content.Update(ctx, cinfo, "labels.containerd.io/gc.ref.snapshot."+unpack.SnapshotterKey)
	if err != nil {
		return err
	}
	log.G(ctx).WithFields(log.Fields{
		"config":   config.Digest,
		"chainID":  chainID,
		"parallel": job.mode == prepareIndependent,
		"duration": time.Since(unpackStart),
	}).Debug("image unpacked")

	return nil
}

// fetch downloads the blobs of layers that are not unpacked. It returns the
// first error.
func (u *Unpacker) fetch(ctx context.Context, h images.Handler, layers []ocispec.Descriptor) error {
	group, ctx := errgroup.WithContext(ctx)
	blobs := newBlobSet(u, h, group)
	for i, desc := range layers {
		blobs.fetch(ctx, i, desc)
	}
	return group.Wait()
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

// preparationMode selects independent preparation when a limiter is configured
// and the snapshotter supports rebasing. The limiter may allow just one apply.
// TODO: check applier compatibility through both direct and proxy calls (#13053).
func (u *Unpacker) preparationMode(unpack *Platform) preparationMode {
	if u.unpackLimiter == nil {
		return prepareWithParent
	}
	if !slices.Contains(unpack.SnapshotterCapabilities, snapshots.RebaseCap) {
		log.L.Infof("snapshotter does not support rebase capability, unpacking will be sequential")
		return prepareWithParent
	}
	return prepareIndependent
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
