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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/containerd/errdefs"

	"github.com/containerd/containerd/v2/core/snapshots"
)

// labelPrefix namespaces the labels the snapshotter records about a snapshot
// for its own use.
//
// The prefix sits outside "containerd.io/snapshot/", whose labels are inherited
// from image annotations (see snapshots.FilterInheritedLabels): a manifest could
// otherwise claim its layer lives at any path on the host. The same filter runs
// on everything the metadata snapshotter passes down. A client cannot set or
// clear these labels either. They stay readable: Stat and Walk report the
// backend's labels alongside the metadata store's.
const labelPrefix = "io.containerd.overlayfs.v1/"

const (
	// layerSourceKindLabel records where a snapshot's layer directory comes from.
	layerSourceKindLabel = labelPrefix + "layer.source"
	// layerSourceRefLabel records which layer directory, in the form the kind
	// defines. A cache ref is an absolute path.
	layerSourceRefLabel = labelPrefix + "layer.ref"
	// layerSourceSizeLabel and layerSourceInodesLabel record the precomputed
	// disk usage of a non-local layer so Commit and active Usage do not walk an
	// external directory tree.
	layerSourceSizeLabel   = labelPrefix + "layer.size"
	layerSourceInodesLabel = labelPrefix + "layer.inodes"
)

// layerSourceKind identifies where a snapshot's layer directory comes from,
// which determines what may be done with it. How a layer is composed into a
// mount does not depend on its kind.
type layerSourceKind string

const (
	// layerSourceLocal is a directory stored in the snapshot directory itself,
	// populated there by a differ. A snapshot that records no source has one.
	layerSourceLocal layerSourceKind = ""

	// layerSourceCache is a directory in an operator-owned layer content cache,
	// shared with every other snapshot of that layer. It is pre-populated, so
	// it is complete before anything is applied. It must never be written to.
	layerSourceCache layerSourceKind = "cache"
)

// layerSource records where a snapshot's layer directory comes from. Ref is
// kind-specific and empty for a local directory; for a cached one it is the
// absolute path of the cached snapshot's fs directory, which is mounted
// directly, and Usage holds the precomputed usage from the cache's metadata.db.
type layerSource struct {
	Kind  layerSourceKind
	Ref   string
	Usage snapshots.Usage
}

// populated reports whether the layer directory is already complete when the
// snapshot is prepared. Nothing is fetched or applied into such a snapshot.
func (l layerSource) populated() bool {
	return l.Kind != layerSourceLocal
}

// owned reports whether the layer directory belongs to this snapshot alone.
// Such a directory may be written, have its attributes changed, and be removed
// with the snapshot. A directory from any other source is shared and must be
// left untouched.
func (l layerSource) owned() bool {
	return l.Kind == layerSourceLocal
}

// labels returns the labels recording this source. A local directory does not
// record one. It returns nil for such a directory.
func (l layerSource) labels() map[string]string {
	if !l.populated() {
		return nil
	}
	m := map[string]string{
		layerSourceKindLabel: string(l.Kind),
		layerSourceRefLabel:  l.Ref,
	}
	if l.Usage.Size != 0 || l.Usage.Inodes != 0 {
		m[layerSourceSizeLabel] = strconv.FormatInt(l.Usage.Size, 10)
		m[layerSourceInodesLabel] = strconv.FormatInt(l.Usage.Inodes, 10)
	}
	return m
}

// layerSourceFromInfo returns the layer source recorded on a snapshot, or a
// local one if the snapshot does not record a source.
func layerSourceFromInfo(info snapshots.Info) (layerSource, error) {
	src := layerSource{
		Kind: layerSourceKind(info.Labels[layerSourceKindLabel]),
		Ref:  info.Labels[layerSourceRefLabel],
	}
	sizeStr, hasSize := info.Labels[layerSourceSizeLabel]
	inodesStr, hasInodes := info.Labels[layerSourceInodesLabel]

	switch src.Kind {
	case layerSourceLocal:
		if src.Ref != "" || hasSize || hasInodes {
			return layerSource{}, fmt.Errorf("snapshot %q records layer source metadata without a source kind", info.Name)
		}
		return layerSource{}, nil
	case layerSourceCache:
		if src.Ref == "" || !filepath.IsAbs(src.Ref) {
			return layerSource{}, fmt.Errorf("snapshot %q records a %q layer source with invalid ref %q", info.Name, src.Kind, src.Ref)
		}
		if hasSize {
			size, err := strconv.ParseInt(sizeStr, 10, 64)
			if err != nil || size < 0 {
				return layerSource{}, fmt.Errorf("snapshot %q records invalid layer size %q", info.Name, sizeStr)
			}
			src.Usage.Size = size
		}
		if hasInodes {
			inodes, err := strconv.ParseInt(inodesStr, 10, 64)
			if err != nil || inodes < 0 {
				return layerSource{}, fmt.Errorf("snapshot %q records invalid layer inodes %q", info.Name, inodesStr)
			}
			src.Usage.Inodes = inodes
		}
		return src, nil
	default:
		// A newer version of the snapshotter recorded a source this one does
		// not implement. Mounting the directory would apply rules this version
		// does not have.
		return layerSource{}, fmt.Errorf("snapshot %q records an unknown layer source %q", info.Name, src.Kind)
	}
}

// privateLabels returns the subset of labels the snapshotter records for
// itself.
func privateLabels(labels map[string]string) map[string]string {
	var private map[string]string
	for k, v := range labels {
		if strings.HasPrefix(k, labelPrefix) {
			if private == nil {
				private = make(map[string]string, 2)
			}
			private[k] = v
		}
	}
	return private
}

// resolveLayerDir returns the path of a snapshot's layer directory together
// with where it came from. A local directory lives in the snapshot directory.
// Any other is mounted from its source. Nothing in the snapshot aliases content
// it does not own.
//
// A recorded source that does not resolve is an error, not a miss. Reporting it
// as absent would let the caller treat the layer as unapplied or mount an empty
// local snapshot directory in its place.
func (o *snapshotter) resolveLayerDir(id string, info snapshots.Info) (string, layerSource, error) {
	src, err := layerSourceFromInfo(info)
	if err != nil {
		return "", layerSource{}, err
	}
	if src.populated() {
		fi, err := os.Stat(src.Ref)
		if err != nil {
			return "", src, fmt.Errorf("layer directory %q from the %s is unusable: %w", src.Ref, src.Kind, err)
		}
		if !fi.IsDir() {
			return "", src, fmt.Errorf("layer directory %q from the %s is not a directory: %w", src.Ref, src.Kind, errdefs.ErrFailedPrecondition)
		}
		return src.Ref, src, nil
	}

	// A local layer directory is a directory this snapshotter created inside the
	// snapshot directory. Lstat answers without following a link. A link would
	// hand back a path outside the directory as though the snapshot owned it.
	layerDir := o.upperPath(id)
	fi, err := os.Lstat(layerDir)
	if err != nil {
		return "", src, fmt.Errorf("failed to stat layer directory %q: %w", layerDir, err)
	}
	if !fi.IsDir() {
		return "", src, fmt.Errorf("layer directory %q is not a directory: %w", layerDir, errdefs.ErrFailedPrecondition)
	}
	return layerDir, src, nil
}
