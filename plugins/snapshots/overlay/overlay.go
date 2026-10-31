//go:build linux

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

package overlay

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/containerd/v2/internal/userns"
	"github.com/containerd/containerd/v2/plugins/snapshots/overlay/overlayutils"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/opencontainers/go-digest"
	bolt "go.etcd.io/bbolt"
)

// upperdirKey is a key of an optional label to each snapshot.
// This optional label of a snapshot contains the location of "upperdir" where
// the change set between this snapshot and its parent is stored.
const upperdirKey = "containerd.io/snapshot/overlay.upperdir"

// SnapshotterConfig is used to configure the overlay snapshotter instance
type SnapshotterConfig struct {
	asyncRemove   bool
	upperdirLabel bool
	ms            MetaStore
	mountOptions  []string
	remapIDs      bool
	slowChown     bool
	// layerContentCaches lists directories of pre-populated overlayfs snapshots.
	// Each is checked one by one; the first hit is recorded as the snapshot's
	// layer source and mounted from the cache. A directory that doesn't exist
	// is treated as a cache miss. Layers missing from all of them are extracted
	// normally.
	layerContentCaches []string
}

// Opt is an option to configure the overlay snapshotter
type Opt func(config *SnapshotterConfig) error

// AsynchronousRemove defers removal of filesystem content until
// the Cleanup method is called. Removals will make the snapshot
// referred to by the key unavailable and make the key immediately
// available for re-use.
func AsynchronousRemove(config *SnapshotterConfig) error {
	config.asyncRemove = true
	return nil
}

// WithUpperdirLabel adds as an optional label
// "containerd.io/snapshot/overlay.upperdir". This stores the location
// of the upperdir that contains the changeset between the labelled
// snapshot and its parent.
func WithUpperdirLabel(config *SnapshotterConfig) error {
	config.upperdirLabel = true
	return nil
}

// WithMountOptions defines the default mount options used for the overlay mount.
// NOTE: Options are not applied to bind mounts.
func WithMountOptions(options []string) Opt {
	return func(config *SnapshotterConfig) error {
		config.mountOptions = append(config.mountOptions, options...)
		return nil
	}
}

type MetaStore interface {
	TransactionContext(ctx context.Context, writable bool) (context.Context, storage.Transactor, error)
	WithTransaction(ctx context.Context, writable bool, fn storage.TransactionCallback) error
	Close() error
}

// WithMetaStore allows the MetaStore to be created outside the snapshotter
// and passed in.
func WithMetaStore(ms MetaStore) Opt {
	return func(config *SnapshotterConfig) error {
		config.ms = ms
		return nil
	}
}

func WithRemapIDs(config *SnapshotterConfig) error {
	config.remapIDs = true
	return nil
}

func WithSlowChown(config *SnapshotterConfig) error {
	config.slowChown = true
	return nil
}

// WithLayerContentCaches configures read-only directories of pre-populated
// overlayfs snapshots that the snapshotter sources layers from on pull instead
// of downloading and extracting them. See the layerContentCaches field for
// details.
func WithLayerContentCaches(paths ...string) Opt {
	return func(config *SnapshotterConfig) error {
		config.layerContentCaches = paths
		return nil
	}
}

type snapshotter struct {
	root               string
	ms                 MetaStore
	asyncRemove        bool
	upperdirLabel      bool
	options            []string
	remapIDs           bool
	slowChown          bool
	layerContentCaches []string

	cacheMu      sync.Mutex
	cacheIndices []atomic.Pointer[dirCacheIndex]
}

// NewSnapshotter returns a Snapshotter which uses overlayfs. The overlayfs
// diffs are stored under the provided root. A metadata file is stored under
// the root.
func NewSnapshotter(root string, opts ...Opt) (snapshots.Snapshotter, error) {
	var config SnapshotterConfig
	for _, opt := range opts {
		if err := opt(&config); err != nil {
			return nil, err
		}
	}

	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	supportsDType, err := fs.SupportsDType(root)
	if err != nil {
		return nil, err
	}
	if !supportsDType {
		return nil, fmt.Errorf("%s does not support d_type. If the backing filesystem is xfs, please reformat with ftype=1 to enable d_type support", root)
	}

	var layerContentCaches []string
	if len(config.layerContentCaches) > 0 {
		// A cache dir is recorded in a snapshot and mounted later. A relative
		// path would resolve against the working directory of whichever
		// process performs the mount. The check is only lexical:
		// dirs are not required to exist, as a missing one just yields a cache
		// miss and may well be provisioned after startup.
		layerContentCaches = make([]string, len(config.layerContentCaches))
		for i, dir := range config.layerContentCaches {
			if !filepath.IsAbs(dir) {
				return nil, fmt.Errorf("layer_content_caches %q must be an absolute path", dir)
			}
			layerContentCaches[i] = filepath.Clean(dir)
		}

		log.L.WithField("dirs", layerContentCaches).Info("overlayfs layer content cache enabled")
	}

	if config.ms == nil {
		config.ms, err = storage.NewMetaStore(filepath.Join(root, "metadata.db"))
		if err != nil {
			return nil, err
		}
	}

	if err := os.Mkdir(filepath.Join(root, "snapshots"), 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}

	if !hasOption(config.mountOptions, "userxattr") {
		// figure out whether "userxattr" option is recognized by the kernel && needed
		userxattr, err := overlayutils.NeedsUserXAttr(root)
		if err != nil {
			log.L.WithError(err).Warnf("cannot detect whether \"userxattr\" option needs to be used, assuming to be %v", userxattr)
		}
		if userxattr {
			config.mountOptions = append(config.mountOptions, "userxattr")
		}
	}

	// Mount options are last-wins in the kernel, so appending "index=off"
	// after a configured "index=on" would silently override it.
	if !hasOption(config.mountOptions, "index") && supportsIndex() {
		config.mountOptions = append(config.mountOptions, "index=off")
	}

	return &snapshotter{
		root:               root,
		ms:                 config.ms,
		asyncRemove:        config.asyncRemove,
		upperdirLabel:      config.upperdirLabel,
		options:            config.mountOptions,
		remapIDs:           config.remapIDs,
		slowChown:          config.slowChown,
		layerContentCaches: layerContentCaches,
		cacheIndices:       make([]atomic.Pointer[dirCacheIndex], len(layerContentCaches)),
	}, nil
}

// hasOption reports whether the "key" option is present in options, either
// as a bare flag ("userxattr") or in "key=value" form ("index=on").
func hasOption(options []string, key string) bool {
	for _, option := range options {
		if option == key {
			return true
		}
		if optionKey, _, ok := strings.Cut(option, "="); ok && optionKey == key {
			return true
		}
	}
	return false
}

// Stat returns the info for an active or committed snapshot by name or
// key.
//
// Should be used for parent resolution, existence checks and to discern
// the kind of snapshot.
func (o *snapshotter) Stat(ctx context.Context, key string) (info snapshots.Info, err error) {
	var id string
	if err := o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		id, info, _, err = storage.GetInfo(ctx, key)
		return err
	}); err != nil {
		return info, err
	}

	if o.upperdirLabel {
		if info.Labels == nil {
			info.Labels = make(map[string]string)
		}
		info.Labels[upperdirKey] = o.upperdirPath(id, info)
	}
	return info, nil
}

func (o *snapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (newInfo snapshots.Info, err error) {
	err = o.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		// A backend snapshotter only receives inherited labels. A caller cannot
		// send the snapshotter's own labels back. An Update that replaces the
		// label set would drop them and leave a snapshot that cannot find its
		// layer directory. They are merged back in here.
		id, cur, _, err := storage.GetInfo(ctx, info.Name)
		if err != nil {
			return err
		}
		if private := privateLabels(cur.Labels); len(private) > 0 {
			if info.Labels == nil {
				info.Labels = make(map[string]string, len(private))
			}
			maps.Copy(info.Labels, private)
		}

		newInfo, err = storage.UpdateInfo(ctx, info, fieldpaths...)
		if err != nil {
			return err
		}

		if o.upperdirLabel {
			if newInfo.Labels == nil {
				newInfo.Labels = make(map[string]string)
			}
			newInfo.Labels[upperdirKey] = o.upperdirPath(id, newInfo)
		}
		return nil
	})
	return newInfo, err
}

// Usage returns the resources taken by the snapshot identified by key.
//
// For active snapshots, this will scan the usage of the overlay "diff" (aka
// "upper") directory and may take some time.
//
// For committed snapshots, the value is returned from the metadata database.
func (o *snapshotter) Usage(ctx context.Context, key string) (_ snapshots.Usage, err error) {
	var (
		usage snapshots.Usage
		info  snapshots.Info
		id    string
	)
	if err := o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		id, info, usage, err = storage.GetInfo(ctx, key)
		return err
	}); err != nil {
		return usage, err
	}

	if info.Kind == snapshots.KindActive {
		upperPath, src, err := o.resolveLayerDir(id, info)
		if err != nil {
			return snapshots.Usage{}, err
		}
		if src.populated() {
			return src.Usage, nil
		}
		du, err := fs.DiskUsage(ctx, upperPath)
		if err != nil {
			// TODO(stevvooe): Consider not reporting an error in this case.
			return snapshots.Usage{}, err
		}
		usage = snapshots.Usage(du)
	}
	return usage, nil
}

func (o *snapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return o.createSnapshot(ctx, snapshots.KindActive, key, parent, opts)
}

func (o *snapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	return o.createSnapshot(ctx, snapshots.KindView, key, parent, opts)
}

// Mounts returns the mounts for the transaction identified by key. Can be
// called on an read-write or readonly transaction.
//
// This can be used to recover mounts after calling View or Prepare.
func (o *snapshotter) Mounts(ctx context.Context, key string) (_ []mount.Mount, err error) {
	var (
		s       storage.Snapshot
		info    snapshots.Info
		parents []snapshots.Info
	)
	if err := o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		s, err = storage.GetSnapshot(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to get active mount: %w", err)
		}

		_, info, _, err = storage.GetInfo(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to get snapshot info: %w", err)
		}

		// Read inside the transaction (see parentInfos).
		parents, err = parentInfos(ctx, info)
		return err
	}); err != nil {
		return nil, err
	}
	return o.mounts(s, info, parents)
}

func (o *snapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	return o.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		// grab the existing id and info
		id, info, _, err := storage.GetInfo(ctx, key)
		if err != nil {
			return err
		}

		layerDir, src, err := o.resolveLayerDir(id, info)
		if err != nil {
			return err
		}

		var usage snapshots.Usage
		if src.populated() {
			usage = src.Usage
		} else {
			du, err := fs.DiskUsage(ctx, layerDir)
			if err != nil {
				return err
			}
			usage = snapshots.Usage(du)
		}

		// storage.CommitActive replaces the snapshot's labels with the ones
		// passed here. The snapshotter's own labels are added back. The
		// committed layer keeps the record of where its directory is.
		if private := privateLabels(info.Labels); len(private) > 0 {
			opts = append(opts, snapshots.WithLabels(private))
		}

		if _, err = storage.CommitActive(ctx, key, name, usage, opts...); err != nil {
			return fmt.Errorf("failed to commit snapshot %s: %w", key, err)
		}
		return nil
	})
}

// Remove abandons the snapshot identified by key. The snapshot will
// immediately become unavailable and unrecoverable. Disk space will
// be freed up on the next call to `Cleanup`.
func (o *snapshotter) Remove(ctx context.Context, key string) (err error) {
	var removals []string
	// Remove directories after the transaction is closed, failures must not
	// return error since the transaction is committed with the removal
	// key no longer available.
	defer func() {
		if err == nil {
			for _, dir := range removals {
				if err := os.RemoveAll(dir); err != nil {
					log.G(ctx).WithError(err).WithField("path", dir).Warn("failed to remove directory")
				}
			}
		}
	}()
	return o.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		_, _, err = storage.Remove(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to remove snapshot %s: %w", key, err)
		}

		if !o.asyncRemove {
			removals, err = o.getCleanupDirectories(ctx)
			if err != nil {
				return fmt.Errorf("unable to get directories for removal: %w", err)
			}
		}
		return nil
	})
}

// Walk the snapshots.
func (o *snapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, fs ...string) error {
	return o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		if o.upperdirLabel {
			return storage.WalkInfo(ctx, func(ctx context.Context, info snapshots.Info) error {
				id, _, _, err := storage.GetInfo(ctx, info.Name)
				if err != nil {
					return err
				}
				if info.Labels == nil {
					info.Labels = make(map[string]string)
				}
				info.Labels[upperdirKey] = o.upperdirPath(id, info)
				return fn(ctx, info)
			}, fs...)
		}
		return storage.WalkInfo(ctx, fn, fs...)
	})
}

// Cleanup cleans up disk resources from removed or abandoned snapshots
func (o *snapshotter) Cleanup(ctx context.Context) error {
	cleanup, err := o.cleanupDirectories(ctx)
	if err != nil {
		return err
	}

	for _, dir := range cleanup {
		if err := os.RemoveAll(dir); err != nil {
			log.G(ctx).WithError(err).WithField("path", dir).Warn("failed to remove directory")
		}
	}

	return nil
}

func (o *snapshotter) cleanupDirectories(ctx context.Context) (_ []string, err error) {
	var cleanupDirs []string
	// Get a write transaction to ensure no other write transaction can be entered
	// while the cleanup is scanning.
	if err := o.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		cleanupDirs, err = o.getCleanupDirectories(ctx)
		return err
	}); err != nil {
		return nil, err
	}
	return cleanupDirs, nil
}

func (o *snapshotter) getCleanupDirectories(ctx context.Context) ([]string, error) {
	ids, err := storage.IDMap(ctx)
	if err != nil {
		return nil, err
	}

	snapshotDir := filepath.Join(o.root, "snapshots")
	fd, err := os.Open(snapshotDir)
	if err != nil {
		return nil, err
	}
	defer fd.Close()

	dirs, err := fd.Readdirnames(0)
	if err != nil {
		return nil, err
	}

	cleanup := []string{}
	for _, d := range dirs {
		if _, ok := ids[d]; ok {
			continue
		}
		cleanup = append(cleanup, filepath.Join(snapshotDir, d))
	}

	return cleanup, nil
}

// parentInfos returns the info of every ancestor of the snapshot described by
// info, nearest first, in the same order as ParentIDs on the corresponding
// storage.Snapshot. Callers read this inside the transaction that read the
// snapshot.
func parentInfos(ctx context.Context, info snapshots.Info) ([]snapshots.Info, error) {
	var parents []snapshots.Info
	for name := info.Parent; name != ""; {
		_, pi, _, err := storage.GetInfo(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("failed to get parent snapshot %q info: %w", name, err)
		}
		parents = append(parents, pi)
		name = pi.Parent
	}
	return parents, nil
}

type cachedLayer struct {
	id     string
	diffID string
	usage  snapshots.Usage
	err    error
}

type dirCacheIndex struct {
	dbPath       string
	snapshotsDir string
	byTarget     map[string][]cachedLayer
}

type cacheHit struct {
	dir   string
	usage snapshots.Usage
}

// resolveCacheLocations locates the metadata.db file and snapshots directory
// for a configured layer_content_caches path. An operator may point the setting
// at an overlayfs snapshotter root (<dir>/metadata.db + <dir>/snapshots), its
// snapshots subdirectory (<dir>/../metadata.db + <dir>), or a containerd root
// directory (<dir>/io.containerd.snapshotter.v1.overlayfs/metadata.db).
func resolveCacheLocations(dir string) (overlayRoot, dbPath, snapshotsDir string, ok bool, err error) {
	dir = filepath.Clean(dir)
	type layout struct {
		root      string
		db        string
		snapshots string
	}
	candidates := []layout{
		{
			root:      dir,
			db:        filepath.Join(dir, "metadata.db"),
			snapshots: filepath.Join(dir, "snapshots"),
		},
	}
	if filepath.Base(dir) == "snapshots" {
		parent := filepath.Dir(dir)
		candidates = append(candidates, layout{
			root:      parent,
			db:        filepath.Join(parent, "metadata.db"),
			snapshots: dir,
		})
	}
	pluginRoot := filepath.Join(dir, "io.containerd.snapshotter.v1.overlayfs")
	candidates = append(candidates, layout{
		root:      pluginRoot,
		db:        filepath.Join(pluginRoot, "metadata.db"),
		snapshots: filepath.Join(pluginRoot, "snapshots"),
	})

	var firstErr error
	for _, c := range candidates {
		fi, statErr := os.Stat(c.db)
		if statErr != nil {
			if !os.IsNotExist(statErr) && firstErr == nil {
				firstErr = statErr
			}
			continue
		}
		if !fi.Mode().IsRegular() {
			continue
		}
		sfi, statErr := os.Stat(c.snapshots)
		if statErr != nil {
			if !os.IsNotExist(statErr) && firstErr == nil {
				firstErr = statErr
			}
			continue
		}
		if sfi.IsDir() {
			return c.root, c.db, c.snapshots, true, nil
		}
	}
	return "", "", "", false, firstErr
}

// cacheID identifies a layer content cache in metrics by its position in the
// configured list and the cache root's directory name. Trailing "snapshots" and
// "io.containerd.snapshotter.v1.overlayfs" components are stripped (when a
// parent directory exists) so all supported layouts aggregate under a meaningful
// name rather than "0:snapshots".
func cacheID(i int, dir string) string {
	p := filepath.Clean(dir)
	if filepath.Base(p) == "snapshots" {
		if parent := filepath.Dir(p); parent != "/" && parent != "." {
			p = parent
		}
	}
	if filepath.Base(p) == "io.containerd.snapshotter.v1.overlayfs" {
		if parent := filepath.Dir(p); parent != "/" && parent != "." {
			p = parent
		}
	}
	return strconv.Itoa(i) + ":" + filepath.Base(p)
}

// getOrBuildCacheIndex returns the in-memory index for a cache directory,
// building and caching it on first successful read. Configured caches are
// immutable once provisioned; directories that do not exist yet or fail to open
// are not cached so a cache mounted after startup is picked up on a later pull.
func (o *snapshotter) getOrBuildCacheIndex(ctx context.Context, i int, dbPath, snapshotsDir string) (*dirCacheIndex, error) {
	o.cacheMu.Lock()
	defer o.cacheMu.Unlock()
	if idx := o.cacheIndices[i].Load(); idx != nil {
		return idx, nil
	}

	idx, err := buildCacheIndex(ctx, dbPath, snapshotsDir)
	if err != nil {
		return nil, err
	}
	o.cacheIndices[i].Store(idx)
	return idx, nil
}

func buildCacheIndex(ctx context.Context, dbPath, snapshotsDir string) (*dirCacheIndex, error) {
	ms, err := storage.NewMetaStore(dbPath, func(bopts *bolt.Options) error {
		bopts.ReadOnly = true
		bopts.Timeout = time.Second
		return nil
	})
	if err != nil {
		return nil, err
	}
	defer ms.Close()

	idx := &dirCacheIndex{
		dbPath:       dbPath,
		snapshotsDir: snapshotsDir,
		byTarget:     make(map[string][]cachedLayer),
	}

	err = ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		return storage.WalkInfo(ctx, func(ctx context.Context, info snapshots.Info) error {
			if info.Kind != snapshots.KindCommitted {
				return nil
			}
			src, srcErr := layerSourceFromInfo(info)
			if srcErr == nil && !src.owned() {
				// Do not re-export layers that a cache snapshotter itself
				// sourced from an external path.
				return nil
			}
			id, _, usage, err := storage.GetInfo(ctx, info.Name)
			if err != nil {
				return err
			}
			entry := cachedLayer{
				id:     id,
				diffID: info.Labels[snapshots.LabelSnapshotDiffID],
				usage:  usage,
				err:    srcErr,
			}
			if ref, ok := info.Labels[snapshots.LabelSnapshotRef]; ok {
				if ref != "" {
					idx.byTarget[ref] = append(idx.byTarget[ref], entry)
				}
				return nil
			}
			idx.byTarget[info.Name] = append(idx.byTarget[info.Name], entry)
			for s := info.Name; ; {
				_, after, ok := strings.Cut(s, "/")
				if !ok {
					break
				}
				if after != "" {
					idx.byTarget[after] = append(idx.byTarget[after], entry)
				}
				s = after
			}
			return nil
		})
	})
	if err != nil && !errdefs.IsNotFound(err) {
		// Note: errdefs.IsNotFound means a freshly initialized metadata.db has
		// no "v1" bucket yet; treat that as an empty cache rather than an error.
		return nil, err
	}
	return idx, nil
}

// lookupCache returns the cached overlayfs layer directory and precomputed usage
// that can serve the layer being prepared, or false on a miss. It gates on: at
// least one cache being configured, the Prepare being an image-layer extraction
// (carries the snapshot.ref and diff-id labels), and the committed snapshot's
// fs directory being present in the cache. Caches are checked one by one and
// the first hit wins. Every miss returns false so pulls keep working.
func (o *snapshotter) lookupCache(ctx context.Context, opts ...snapshots.Opt) (cacheHit, bool) {
	if len(o.layerContentCaches) == 0 {
		return cacheHit{}, false
	}

	var base snapshots.Info
	for _, opt := range opts {
		if err := opt(&base); err != nil {
			return cacheHit{}, false
		}
	}

	target := base.Labels[snapshots.LabelSnapshotRef]
	diffIDStr := base.Labels[snapshots.LabelSnapshotDiffID]
	if target == "" || diffIDStr == "" {
		// Not an image-layer extraction, or no diffID/ref to key on.
		return cacheHit{}, false
	}
	diffID, err := digest.Parse(diffIDStr)
	if err != nil {
		log.G(ctx).WithError(err).WithField("diffID", diffIDStr).
			Warn("overlayfs layer cache: invalid diff-id label, treating as cache miss")
		return cacheHit{}, false
	}

	for i, dir := range o.layerContentCaches {
		if hit, ok := o.lookupCacheInDir(ctx, i, dir, target, diffID.String()); ok {
			cacheLookups.WithValues("hit").Inc()
			cacheHits.WithValues(cacheID(i, dir)).Inc()
			if hit.usage.Size > 0 {
				cacheHitBytes.Inc(float64(hit.usage.Size))
			}
			return hit, true
		}
	}

	cacheLookups.WithValues("miss").Inc()

	log.G(ctx).WithFields(log.Fields{
		"chainID": target,
		"diffID":  diffID.String(),
	}).Trace("overlayfs layer cache miss")
	return cacheHit{}, false
}

func (o *snapshotter) lookupCacheInDir(ctx context.Context, i int, dir, target, diffIDStr string) (cacheHit, bool) {
	id := cacheID(i, dir)
	idx := o.cacheIndices[i].Load()
	if idx == nil {
		dir = filepath.Clean(dir)
		overlayRoot, dbPath, snapshotsDir, ok, err := resolveCacheLocations(dir)
		if err != nil {
			cacheErrors.WithValues("unreadable", id).Inc()
			log.G(ctx).WithError(err).WithField("dir", dir).
				Warn("overlayfs layer cache: failed to stat cache directory, skipping this cache")
			return cacheHit{}, false
		}
		if !ok {
			return cacheHit{}, false
		}
		if filepath.Clean(overlayRoot) == filepath.Clean(o.root) {
			return cacheHit{}, false
		}

		idx, err = o.getOrBuildCacheIndex(ctx, i, dbPath, snapshotsDir)
		if err != nil {
			cacheErrors.WithValues("unreadable", id).Inc()
			log.G(ctx).WithError(err).WithField("db", dbPath).
				Warn("overlayfs layer cache: failed to open or query cache metadata.db, skipping this cache")
			return cacheHit{}, false
		}
	}

	return matchCacheEntries(ctx, id, idx.dbPath, idx.snapshotsDir, diffIDStr, idx.byTarget[target])
}

func matchCacheEntries(ctx context.Context, cacheLabel, dbPath, snapshotsDir, diffIDStr string, entries []cachedLayer) (cacheHit, bool) {
	for _, entry := range entries {
		if entry.diffID != "" && entry.diffID != diffIDStr {
			continue
		}
		if entry.err != nil {
			cacheErrors.WithValues("unreadable", cacheLabel).Inc()
			log.G(ctx).WithError(entry.err).WithField("db", dbPath).
				Warn("overlayfs layer cache: invalid layer source metadata in cache, skipping entry")
			continue
		}

		candidate := filepath.Join(snapshotsDir, entry.id, "fs")
		fi, err := os.Lstat(candidate)
		if err != nil {
			if !os.IsNotExist(err) {
				cacheErrors.WithValues("unreadable", cacheLabel).Inc()
				log.G(ctx).WithError(err).WithField("dir", candidate).
					Warn("overlayfs layer cache: failed to stat cached layer directory, skipping entry")
			}
			continue
		}
		if !fi.IsDir() {
			cacheErrors.WithValues("unreadable", cacheLabel).Inc()
			log.G(ctx).WithField("dir", candidate).
				Warn("overlayfs layer cache: cached layer path is not a directory, skipping entry")
			continue
		}
		return cacheHit{dir: candidate, usage: entry.usage}, true
	}
	return cacheHit{}, false
}

// createSnapshot creates an active (or view) snapshot and returns its mounts.
// On an image-layer extraction whose committed layer is in the layer content
// cache, it records the cache entry as the snapshot's layer source and returns
// it as a read-only mount. The unpacker reads that as a staged layer, skips the
// layer extraction, and commits the snapshot normally. The parent is applied
// either at Prepare (sequential unpacking) or at Commit via WithParent
// (parallel unpacking).
func (o *snapshotter) createSnapshot(ctx context.Context, kind snapshots.Kind, key, parent string, opts []snapshots.Opt) (_ []mount.Mount, err error) {
	var (
		s        storage.Snapshot
		td, path string
		info     snapshots.Info
		parents  []snapshots.Info
		mounts   []mount.Mount
	)

	if kind == snapshots.KindActive {
		if hit, ok := o.lookupCache(ctx, opts...); ok {
			log.G(ctx).WithFields(log.Fields{
				"key": key,
				"dir": hit.dir,
			}).Debug("layer content cache hit, serving the cached overlayfs layer")
			opts = append(opts, snapshots.WithLabels(
				layerSource{Kind: layerSourceCache, Ref: hit.dir, Usage: hit.usage}.labels()))
		}
	}

	defer func() {
		if err != nil {
			if td != "" {
				if err1 := os.RemoveAll(td); err1 != nil {
					log.G(ctx).WithError(err1).Warn("failed to cleanup temp snapshot directory")
				}
			}
			if path != "" {
				if err1 := os.RemoveAll(path); err1 != nil {
					log.G(ctx).WithError(err1).WithField("path", path).Error("failed to reclaim snapshot directory, directory may need removal")
					err = fmt.Errorf("failed to remove path: %v: %w", err1, err)
				}
			}
		}
	}()

	if err = o.ms.WithTransaction(ctx, true, func(ctx context.Context) (err error) {
		snapshotDir := filepath.Join(o.root, "snapshots")
		td, err = o.prepareDirectory(ctx, snapshotDir, kind)
		if err != nil {
			return fmt.Errorf("failed to create prepare snapshot dir: %w", err)
		}

		s, err = storage.CreateSnapshot(ctx, kind, key, parent, opts...)
		if err != nil {
			return fmt.Errorf("failed to create snapshot: %w", err)
		}

		_, info, _, err = storage.GetInfo(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to get snapshot info: %w", err)
		}

		// Read inside the transaction (see parentInfos).
		parents, err = parentInfos(ctx, info)
		if err != nil {
			return err
		}

		var (
			mappedUID, mappedGID     = -1, -1
			uidmapLabel, gidmapLabel string
			needsRemap               = false
		)
		// NOTE: if idmapped mounts' supported by hosted kernel there may be
		// no parents at all, so overlayfs will not work and snapshotter
		// will use bind mount. To be able to create file objects inside the
		// rootfs -- just chown this only bound directory according to provided
		// {uid,gid}map. In case of one/multiple parents -- chown upperdir.
		if v, ok := info.Labels[snapshots.LabelSnapshotUIDMapping]; ok {
			uidmapLabel = v
			needsRemap = true
		}
		if v, ok := info.Labels[snapshots.LabelSnapshotGIDMapping]; ok {
			gidmapLabel = v
			needsRemap = true
		}

		if needsRemap {
			var idMap userns.IDMap
			if err = idMap.Unmarshal(uidmapLabel, gidmapLabel); err != nil {
				return fmt.Errorf("failed to unmarshal snapshot ID mapped labels: %w", err)
			}
			root, err := idMap.RootPair()
			if err != nil {
				return fmt.Errorf("failed to find root pair: %w", err)
			}
			mappedUID, mappedGID = int(root.Uid), int(root.Gid)
		}

		if mappedUID == -1 || mappedGID == -1 {
			if len(s.ParentIDs) > 0 {
				parentDir, _, err := o.resolveLayerDir(s.ParentIDs[0], parents[0])
				if err != nil {
					return fmt.Errorf("failed to resolve parent layer directory: %w", err)
				}
				st, err := os.Stat(parentDir)
				if err != nil {
					return fmt.Errorf("failed to stat parent: %w", err)
				}
				stat, ok := st.Sys().(*syscall.Stat_t)
				if !ok {
					return errors.New("incompatible types after stat call: *syscall.Stat_t expected")
				}
				mappedUID = int(stat.Uid)
				mappedGID = int(stat.Gid)
			}
		}

		if mappedUID != -1 && mappedGID != -1 {
			if err := os.Lchown(filepath.Join(td, "fs"), mappedUID, mappedGID); err != nil {
				return fmt.Errorf("failed to chown: %w", err)
			}
		}

		targetPath := filepath.Join(snapshotDir, s.ID)
		if err = os.Rename(td, targetPath); err != nil {
			return fmt.Errorf("failed to rename: %w", err)
		}
		td = ""
		path = targetPath

		mounts, err = o.mounts(s, info, parents)
		if err != nil {
			return err
		}

		return nil
	}); err != nil {
		return nil, err
	}
	return mounts, nil
}

func (o *snapshotter) prepareDirectory(ctx context.Context, snapshotDir string, kind snapshots.Kind) (string, error) {
	td, err := os.MkdirTemp(snapshotDir, "new-")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	if err := os.Mkdir(filepath.Join(td, "fs"), 0755); err != nil {
		return td, err
	}

	if kind == snapshots.KindActive {
		if err := os.Mkdir(filepath.Join(td, "work"), 0711); err != nil {
			return td, err
		}
	}

	return td, nil
}

func (o *snapshotter) mounts(s storage.Snapshot, info snapshots.Info, parents []snapshots.Info) ([]mount.Mount, error) {
	var options []string

	if o.remapIDs {
		if v, ok := info.Labels[snapshots.LabelSnapshotUIDMapping]; ok {
			options = append(options, "uidmap="+v)
		}
		if v, ok := info.Labels[snapshots.LabelSnapshotGIDMapping]; ok {
			options = append(options, "gidmap="+v)
		}
	}

	if len(s.ParentIDs) == 0 {
		// A recorded layer source that does not resolve is an error here too
		// (see resolveLayerDir): falling through would hand out a writable
		// mount for a layer the snapshotter has already reported complete.
		topDir, src, err := o.resolveLayerDir(s.ID, info)
		if err != nil {
			return nil, err
		}
		// if we only have one layer/no parents then just return a bind mount as overlay
		// will not work
		roFlag := "rw"
		if s.Kind == snapshots.KindView || src.populated() {
			roFlag = "ro"
		}
		return []mount.Mount{
			{
				Source: topDir,
				Type:   "bind",
				Options: append(options,
					roFlag,
					"rbind",
				),
			},
		}, nil
	}

	if len(parents) != len(s.ParentIDs) {
		return nil, fmt.Errorf("got %d parent infos for %d parents of snapshot %s: %w",
			len(parents), len(s.ParentIDs), s.ID, errdefs.ErrInvalidArgument)
	}

	parentPaths := make([]string, len(s.ParentIDs))
	for i := range s.ParentIDs {
		parentDir, _, err := o.resolveLayerDir(s.ParentIDs[i], parents[i])
		if err != nil {
			return nil, err
		}
		parentPaths[i] = parentDir
	}

	if s.Kind == snapshots.KindActive {
		topDir, src, err := o.resolveLayerDir(s.ID, info)
		if err != nil {
			return nil, err
		}
		if src.populated() {
			// Nothing in this overlay is writable. The cached layer is stacked
			// as the top lower above its parents with no upperdir, and "ro"
			// states that explicitly so the unpacker treats it as staged.
			parentPaths = append([]string{topDir}, parentPaths...)
			options = append(options, "ro")
		} else {
			options = append(options,
				"workdir="+o.workPath(s.ID),
				"upperdir="+topDir,
			)
		}
	} else if len(s.ParentIDs) == 1 {
		return []mount.Mount{
			{
				Source: parentPaths[0],
				Type:   "bind",
				Options: append(options,
					"ro",
					"rbind",
				),
			},
		}, nil
	}

	options = append(options, "lowerdir="+strings.Join(parentPaths, ":"))
	options = append(options, o.options...)

	return []mount.Mount{
		{
			Type:    "overlay",
			Source:  "overlay",
			Options: options,
		},
	}, nil
}

func (o *snapshotter) upperPath(id string) string {
	return filepath.Join(o.root, "snapshots", id, "fs")
}

func (o *snapshotter) upperdirPath(id string, info snapshots.Info) string {
	if src, err := layerSourceFromInfo(info); err == nil && src.populated() {
		return src.Ref
	}
	return o.upperPath(id)
}

func (o *snapshotter) workPath(id string) string {
	return filepath.Join(o.root, "snapshots", id, "work")
}

// Close closes the snapshotter
func (o *snapshotter) Close() error {
	return o.ms.Close()
}

// supportsIndex checks whether the "index=off" option is supported by the kernel.
func supportsIndex() bool {
	if _, err := os.Stat("/sys/module/overlay/parameters/index"); err == nil {
		return true
	}
	return false
}
