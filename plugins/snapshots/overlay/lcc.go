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
	"syscall"
	"time"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/log"
	digest "github.com/opencontainers/go-digest"
)

const (
	// When LCC is enabled, labelSnapshotUID and labelSnapshotGID record the uid/gid used to
	// select the per-ownership cache content directory for this snapshot.
	labelSnapshotUID = "containerd.io/snapshot.uid"
	labelSnapshotGID = "containerd.io/snapshot.gid"
)

// layerContentCache holds per-snapshotter LCC configuration and runtime state.
type layerContentCache struct {
	root    string
	enabled bool
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
// raw diffID string and string-form uid/gid. The layout is:
// <root>/cache/<algorithm>.<hex>.<uid>.<gid>/
func (lcc *layerContentCache) contentPathFromParts(diffID string, uid, gid int) (string, error) {
	d, err := digest.Parse(diffID)
	if err != nil {
		return "", fmt.Errorf("invalid lcc diffID %q: %w", diffID, err)
	}
	name := fmt.Sprintf("%s.%s.%d.%d", d.Algorithm(), d.Encoded(), uid, gid)
	return filepath.Join(lcc.root, "cache", name), nil
}

// contentPath returns the shared content directory for the snapshot. The
// layout is: <root>/cache/<algorithm>.<hex>.<uid>.<gid>/, e.g.
// <root>/cache/sha256.abc123....0.0 for diffID "sha256:abc123..." owned by root.
func (lcc *layerContentCache) contentPath(info snapshots.Info) (string, error) {
	diffID := info.Labels[snapshots.LabelSnapshotDiffID]
	uidStr := info.Labels[labelSnapshotUID]
	gidStr := info.Labels[labelSnapshotGID]
	if diffID == "" || uidStr == "" || gidStr == "" {
		return "", fmt.Errorf("missing lcc labels for snapshot %q", info.Name)
	}
	uid, err := strconv.Atoi(uidStr)
	if err != nil {
		return "", fmt.Errorf("invalid lcc uid label %q: %w", uidStr, err)
	}
	gid, err := strconv.Atoi(gidStr)
	if err != nil {
		return "", fmt.Errorf("invalid lcc gid label %q: %w", gidStr, err)
	}
	return lcc.contentPathFromParts(diffID, uid, gid)
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
func (lcc *layerContentCache) computeLabels(diffID string, uid, gid int) (map[string]string, error) {
	if uid == -1 || gid == -1 {
		uid, gid = os.Getuid(), os.Getgid()
	}

	contentPath, err := lcc.contentPathFromParts(diffID, uid, gid)
	if err != nil {
		return nil, err
	}

	labels := map[string]string{
		labelSnapshotUID: strconv.Itoa(uid),
		labelSnapshotGID: strconv.Itoa(gid),
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
// into CommitActive. Both the diff-ID and the uid/gid ownership labels are
// re-injected from the active snapshot's info because CommitActive replaces
// labels entirely with whatever opts supply; callers (e.g. pkg/rootfs/apply.go)
// may not forward the diff-ID to Commit, so we preserve it here explicitly.
func (lcc *layerContentCache) commitOpts(info snapshots.Info) []snapshots.Opt {
	return []snapshots.Opt{snapshots.WithLabels(map[string]string{
		snapshots.LabelSnapshotDiffID: info.Labels[snapshots.LabelSnapshotDiffID],
		labelSnapshotUID:              info.Labels[labelSnapshotUID],
		labelSnapshotGID:              info.Labels[labelSnapshotGID],
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
