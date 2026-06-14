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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	digest "github.com/opencontainers/go-digest"
)

const (
	// When LCC is enabled, these labels are set on every LCC snapshot to fully
	// identify its cache content directory without requiring a chain walk.
	labelSnapshotUID       = "containerd.io/snapshot.uid"
	labelSnapshotGID       = "containerd.io/snapshot.gid"
	labelSnapshotDiffIDSeq = "containerd.io/snapshot.diffID.seq"
)

// layerContentCache holds per-snapshotter LCC configuration and runtime state.
type layerContentCache struct {
	sync.RWMutex
	root     string
	enabled  bool
	snapInfo map[string]lccSnapshotInfo
}

type lccSnapshotInfo struct {
	name   string
	parent string
	diffID string
}

// newLayerContentCache constructs a layerContentCache and, when enabled, pre-populates
// snapInfo by walking all committed snapshots so that diffIDSeqInChain is ready before
// the first Prepare call.
func newLayerContentCache(ctx context.Context, ms MetaStore, root string, enabled bool) (*layerContentCache, error) {
	lcc := &layerContentCache{
		root:    root,
		enabled: enabled,
	}
	if !enabled {
		return lcc, nil
	}

	// IsNotFound means the storage bucket doesn't exist yet (fresh database) — treat as empty.
	lcc.snapInfo = make(map[string]lccSnapshotInfo)
	if err := ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		return storage.WalkInfo(ctx, func(_ context.Context, info snapshots.Info) error {
			if info.Kind != snapshots.KindCommitted {
				return nil
			}
			lcc.snapInfo[info.Name] = lccSnapshotInfo{
				info.Name,
				info.Parent,
				info.Labels[snapshots.LabelSnapshotDiffID],
			}
			return nil
		})
	}); err != nil && !errdefs.IsNotFound(err) {
		return nil, fmt.Errorf("failed to walk snapshots for lcc cache: %w", err)
	}

	return lcc, nil
}

// WithLayerContentCache enables layer content caching on the snapshotter.
func WithLayerContentCache(config *SnapshotterConfig) error {
	config.lccEnabled = true
	return nil
}

// hasDiffID reports whether info carries a layer diff-ID label, which is set by
// the unpacker on layer-unpack Prepare calls but absent on container-start
// Prepare calls. Only snapshots with a diff-ID should go through the LCC path.
func hasDiffID(info snapshots.Info) bool {
	return info.Labels[snapshots.LabelSnapshotDiffID] != ""
}

// hasCacheDir reports whether a snapshot participates in LCC and its shared
// cache content path can be derived from it. labelSnapshotUID is the authoritative
// marker: it is set by lcc.computeLabels at CreateSnapshot time (for both cache
// hits and misses) and re-injected by lcc.commitOpts at CommitActive.
// Checking all labels that lcc.contentPath needs would be wrong: if labelSnapshotUID
// is present but another label is missing, that is a data-consistency bug that
// should surface as an error, not be silently skipped.
func hasCacheDir(info snapshots.Info) bool {
	return info.Labels[labelSnapshotUID] != ""
}

// contentPathFromParts computes the shared content directory path from the
// raw diffID string, uid/gid, and sequence number. The layout is:
// <root>/cache/<algorithm>.<hex>.<uid>.<gid>.<seq>/
func (lcc *layerContentCache) contentPathFromParts(diffID string, uid, gid, seq int) (string, error) {
	d, err := digest.Parse(diffID)
	if err != nil {
		return "", fmt.Errorf("invalid lcc diffID %q: %w", diffID, err)
	}
	name := fmt.Sprintf("%s.%s.%d.%d.%d", d.Algorithm(), d.Encoded(), uid, gid, seq)
	return filepath.Join(lcc.root, "cache", name), nil
}

// contentPath returns the shared content directory for the snapshot. The
// layout is: <root>/cache/<algorithm>.<hex>.<uid>.<gid>.<seq>/, e.g.
// <root>/cache/sha256.abc123....0.0.0 for diffID "sha256:abc123..." owned by
// root appearing for the first time in the layer chain.
func (lcc *layerContentCache) contentPath(info snapshots.Info) (string, error) {
	diffID := info.Labels[snapshots.LabelSnapshotDiffID]
	uidStr := info.Labels[labelSnapshotUID]
	gidStr := info.Labels[labelSnapshotGID]
	seqStr := info.Labels[labelSnapshotDiffIDSeq]
	if diffID == "" || uidStr == "" || gidStr == "" || seqStr == "" {
		return "", fmt.Errorf("snapshot %q missing lcc labels (got %v)", info.Name, info.Labels)
	}
	uid, err := strconv.Atoi(uidStr)
	if err != nil {
		return "", fmt.Errorf("invalid lcc uid label %q: %w", uidStr, err)
	}
	gid, err := strconv.Atoi(gidStr)
	if err != nil {
		return "", fmt.Errorf("invalid lcc gid label %q: %w", gidStr, err)
	}
	seq, err := strconv.Atoi(seqStr)
	if err != nil {
		return "", fmt.Errorf("invalid lcc seq label %q: %w", seqStr, err)
	}
	return lcc.contentPathFromParts(diffID, uid, gid, seq)
}

// diffIDSeqInChain returns the number of times diffID appears in the snapshot
// chain leading up to and including parent. For a new snapshot being prepared on
// top of parent, this is the sequence number that makes its cache path unique when
// the same diffID recurs in the layer chain. Returns 0 for an empty parent.
func (lcc *layerContentCache) diffIDSeqInChain(parent, diffID string) int {
	lcc.RLock()
	defer lcc.RUnlock()
	count := 0
	for cur := parent; cur != ""; cur = lcc.snapInfo[cur].parent {
		if lcc.snapInfo[cur].diffID == diffID {
			count++
		}
	}
	return count
}

// addSnapInfo adds a newly committed snapshot to snapInfo so that future
// diffIDSeqInChain calls using name as parent return correct results. It
// returns a rollback closure that restores the prior state of snapInfo[name];
// the caller uses this in a deferred error handler so that a failed Commit
// undoes the in-memory write. When name already had an entry (e.g. a parallel
// committer raced to commit the same chainID first, causing CommitActive to
// return AlreadyExists), rollback restores that prior entry instead of deleting
// it.
func (lcc *layerContentCache) addSnapInfo(name, parent, diffID string) func() {
	lcc.Lock()
	defer lcc.Unlock()
	prev, existed := lcc.snapInfo[name]
	si := lccSnapshotInfo{
		name:   name,
		parent: parent,
		diffID: diffID,
	}
	lcc.snapInfo[name] = si
	return func() {
		lcc.Lock()
		defer lcc.Unlock()
		if existed {
			lcc.snapInfo[name] = prev
		} else {
			delete(lcc.snapInfo, name)
		}
	}
}

// deleteSnapInfo removes a snapshot from snapInfo when it is removed from the
// snapshotter so that stale entries do not accumulate.
func (lcc *layerContentCache) deleteSnapInfo(name string) {
	lcc.Lock()
	defer lcc.Unlock()
	delete(lcc.snapInfo, name)
}

// orphanedContentPath returns a staging path derived from the given cache
// content path by appending a nanosecond epoch timestamp and the ".orphaned"
// suffix. Two concurrent losers in the same nanosecond would collide, but
// os.Rename in that case would simply fail and be detected by the caller.
func (lcc *layerContentCache) orphanedContentPath(path string) string {
	return fmt.Sprintf("%s.%d.orphaned", path, time.Now().UnixNano())
}

// computeLabels returns the labels to inject at CreateSnapshot time: uid/gid
// ownership labels and, if the shared content directory already exists,
// LabelSkipApply. Labels are returned as a map so the caller can fold them into
// the opts passed to storage.CreateSnapshot, preserving the Created == Updated
// invariant (no storage.UpdateInfo call needed after creation).
func (lcc *layerContentCache) computeLabels(parent, diffID string, uid, gid int) (map[string]string, error) {
	seq := lcc.diffIDSeqInChain(parent, diffID)

	if uid == -1 || gid == -1 {
		uid, gid = os.Getuid(), os.Getgid()
	}

	contentPath, err := lcc.contentPathFromParts(diffID, uid, gid, seq)
	if err != nil {
		return nil, err
	}

	labels := map[string]string{
		labelSnapshotUID:       strconv.Itoa(uid),
		labelSnapshotGID:       strconv.Itoa(gid),
		labelSnapshotDiffIDSeq: strconv.Itoa(seq),
	}

	f, err := os.Stat(contentPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat lcc content path %q: %w", contentPath, err)
	}
	if err == nil && !f.IsDir() {
		return nil, fmt.Errorf("lcc content path %q exists but is not a directory", contentPath)
	}
	if err == nil {
		labels[snapshots.LabelSkipApply] = "true"
	}

	return labels, nil
}

// commitOpts returns opts that carry the labels needed by lcc.contentPath
// into CommitActive. The diff-ID, uid/gid ownership, and sequence number are all
// re-injected from the active snapshot's info because CommitActive replaces
// labels entirely with whatever opts supply; callers (e.g. pkg/rootfs/apply.go)
// may not forward the diff-ID to Commit, so we preserve it here explicitly.
func (lcc *layerContentCache) commitOpts(info snapshots.Info) []snapshots.Opt {
	return []snapshots.Opt{snapshots.WithLabels(map[string]string{
		snapshots.LabelSnapshotDiffID: info.Labels[snapshots.LabelSnapshotDiffID],
		labelSnapshotUID:              info.Labels[labelSnapshotUID],
		labelSnapshotGID:              info.Labels[labelSnapshotGID],
		labelSnapshotDiffIDSeq:        info.Labels[labelSnapshotDiffIDSeq],
	})}
}

// prepareFSDir sets up the fs/ subdirectory within td. If LabelSkipApply is
// set, fs/ is created as a relative symlink to the shared cache content path;
// the symlink is relative so the snapshot tree remains valid if the containerd
// root is relocated. Otherwise fs/ is created as a regular directory for extraction.
func (lcc *layerContentCache) prepareFSDir(info snapshots.Info, td string) error {
	contentPath, err := lcc.contentPath(info)
	if err != nil {
		return err
	}
	if info.Labels[snapshots.LabelSkipApply] == "true" {
		relPath, err := filepath.Rel(td, contentPath)
		if err != nil {
			return fmt.Errorf("failed to compute relative lcc cache path: %w", err)
		}
		return os.Symlink(relPath, filepath.Join(td, "fs"))
	}
	return os.Mkdir(filepath.Join(td, "fs"), 0755)
}

// commitFSDir ensures the snapshot's fs/ directory is a symlink to the shared
// cache content path. If fs/ is already a symlink (skip-apply path), there is
// nothing to do. If fs/ holds extracted content, it is atomically renamed to
// the cache path; if another extractor won the race, our copy is renamed to an
// .orphaned staging path — an O(1) operation that avoids a potentially expensive
// os.RemoveAll inside the bolt transaction. The next Cleanup call removes any
// .orphaned entries. The symlink is relative so the snapshot tree remains valid
// if the containerd root is relocated. Must be called before CommitActive so
// that fs/ is still in place.
func (lcc *layerContentCache) commitFSDir(id string, info snapshots.Info) error {
	upperFSPath := filepath.Join(lcc.root, "snapshots", id, "fs")
	if f, err := os.Lstat(upperFSPath); err == nil && f.Mode()&os.ModeSymlink != 0 {
		return nil
	}

	contentPath, err := lcc.contentPath(info)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(contentPath), 0700); err != nil {
		return err
	}

	// Linux rename(2) returns ENOTEMPTY or EEXIST when the destination is a
	// non-empty directory. Either means a concurrent extractor won the race.
	if err := os.Rename(upperFSPath, contentPath); err != nil {
		if !errors.Is(err, syscall.ENOTEMPTY) && !errors.Is(err, syscall.EEXIST) {
			return fmt.Errorf("failed to promote layer content to lcc cache %s: %w", contentPath, err)
		}
		if err := os.Rename(upperFSPath, lcc.orphanedContentPath(contentPath)); err != nil {
			return fmt.Errorf("failed to stage orphaned lcc layer content: %w", err)
		}
	}

	relPath, err := filepath.Rel(filepath.Dir(upperFSPath), contentPath)
	if err != nil {
		return fmt.Errorf("failed to compute relative lcc cache path: %w", err)
	}

	// If Symlink fails, this function returns an error, which causes Commit's
	// transaction to roll back — the snapshot is never committed. The renamed
	// cache directory becomes an unreferenced entry that the next Cleanup call
	// removes via orphanedPaths. No additional rollback is needed.
	return os.Symlink(relPath, upperFSPath)
}

// orphanedPaths identifies cache directories no longer referenced by any
// snapshot, renames each one to an .orphaned staging path within the transaction,
// and returns the renamed paths for deletion by the caller. Renaming within the
// transaction closes the race where a concurrent Prepare could observe the directory
// still present, create a symlink to it, and then have the symlink broken when the
// caller removes the directory outside the transaction; after the rename, os.Stat
// on the original path fails and Prepare takes a cache miss instead. Any *.orphaned
// entries left by a previous call are also returned so they are retried; referenced
// *.orphaned entries (a bug, but defensive) are skipped. Must be called within a
// bolt write transaction.
func (lcc *layerContentCache) orphanedPaths(ctx context.Context) ([]string, error) {
	referenced := map[string]struct{}{}
	if err := storage.WalkInfo(ctx, func(_ context.Context, info snapshots.Info) error {
		if !hasCacheDir(info) {
			return nil
		}
		path, err := lcc.contentPath(info)
		if err != nil {
			return err
		}
		referenced[path] = struct{}{}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk snapshots for lcc cleanup: %w", err)
	}

	cacheRoot := filepath.Join(lcc.root, "cache")
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var orphaned []string
	for _, e := range entries {
		if !e.IsDir() {
			log.G(ctx).WithField("path", filepath.Join(cacheRoot, e.Name())).Warn("unexpected non-directory entry in lcc cache root; skipping")
			continue
		}
		name := e.Name()
		path := filepath.Join(cacheRoot, name)

		if _, ok := referenced[path]; ok {
			continue
		}

		// Already staged for removal by a prior orphanedPaths or commitFSDir
		// call — collect it for removal.
		if strings.HasSuffix(name, ".orphaned") {
			orphaned = append(orphaned, path)
			continue
		}

		// Rename within the transaction so that any Prepare starting after
		// this transaction commits will see the path absent and take a cache miss.
		staged := lcc.orphanedContentPath(path)
		if err := os.Rename(path, staged); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("failed to stage orphaned lcc cache dir %s: %w", path, err)
		}
		orphaned = append(orphaned, staged)
	}

	return orphaned, nil
}
