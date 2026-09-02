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

package erofs

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/containerd/containerd/v2/core/snapshots"
)

// labelPrefix namespaces the labels the snapshotter records about a snapshot
// for its own use.
//
// It is deliberately not a "containerd.io/snapshot/" label: those are inherited
// from image annotations (see snapshots.FilterInheritedLabels), so a manifest
// could otherwise claim its layer lives at any path on the host. The metadata
// snapshotter filters this prefix out in both directions, which means no client
// and no image can set it, and equally that it is invisible to callers.
const labelPrefix = "io.containerd.erofs.v1/"

const (
	// blobSourceKindLabel records where a snapshot's layer blob comes from.
	blobSourceKindLabel = labelPrefix + "blob.source"
	// blobSourceRefLabel records which blob, in terms meaningful to the kind.
	blobSourceRefLabel = labelPrefix + "blob.ref"
)

// blobSourceKind identifies where a snapshot's layer blob comes from, which
// determines what may be done with it rather than how it is composed into a
// mount (that is the layer's own business, not its storage's).
type blobSourceKind string

const (
	// blobSourceLocal is a blob stored in the snapshot directory itself,
	// written there by a differ. It is never recorded: it is what the absence
	// of a recorded source means.
	blobSourceLocal blobSourceKind = ""

	// blobSourceCache is a blob in an operator-owned layer content cache,
	// shared with every other snapshot of that layer and pre-converted, so it
	// is complete before anything is applied and must never be written to.
	blobSourceCache blobSourceKind = "cache"
)

// blobSource records where a snapshot's layer blob comes from. Ref is
// kind-specific and empty for a local blob; for a cached one it is the absolute
// path of the cache entry, which is mounted directly.
type blobSource struct {
	Kind blobSourceKind
	Ref  string
}

// populated reports whether the blob is already complete when the snapshot is
// prepared. Nothing needs to be fetched or applied into such a snapshot, and
// committing it converts nothing.
func (b blobSource) populated() bool {
	return b.Kind != blobSourceLocal
}

// owned reports whether the blob belongs to this snapshot alone, and so may be
// written, have its attributes changed, or be removed with it. A blob from any
// other source is shared and must be left untouched.
func (b blobSource) owned() bool {
	return b.Kind == blobSourceLocal
}

// labels returns the labels recording this source, or nil for a local blob,
// which records nothing.
func (b blobSource) labels() map[string]string {
	if !b.populated() {
		return nil
	}
	return map[string]string{
		blobSourceKindLabel: string(b.Kind),
		blobSourceRefLabel:  b.Ref,
	}
}

// blobSourceFromInfo returns the blob source recorded on a snapshot, or a local
// one if it records none.
func blobSourceFromInfo(info snapshots.Info) (blobSource, error) {
	src := blobSource{
		Kind: blobSourceKind(info.Labels[blobSourceKindLabel]),
		Ref:  info.Labels[blobSourceRefLabel],
	}
	switch src.Kind {
	case blobSourceLocal:
		if src.Ref != "" {
			return blobSource{}, fmt.Errorf("snapshot %q records a layer blob ref with no source", info.Name)
		}
		return blobSource{}, nil
	case blobSourceCache:
		if src.Ref == "" {
			return blobSource{}, fmt.Errorf("snapshot %q records a %s layer blob with no ref", info.Name, src.Kind)
		}
		return src, nil
	default:
		// Written by a version that knows a source this one does not. Guessing
		// would mean mounting a blob under rules we do not implement.
		return blobSource{}, fmt.Errorf("snapshot %q records an unknown layer blob source %q", info.Name, src.Kind)
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

// errNoLayerBlob is returned by resolveBlob for a snapshot that holds no layer
// blob at all, which is the normal state of an active snapshot before a differ
// has applied anything into it. It is deliberately distinct from a blob that is
// recorded but unusable, which callers must not mistake for one that was never
// there.
var errNoLayerBlob = errors.New("no erofs layer blob")

// resolveBlob returns the path of a snapshot's layer blob together with where
// it came from. A local blob lives in the snapshot directory; any other is
// mounted from its source directly, so nothing in the snapshot aliases content
// it does not own.
//
// A recorded blob that no longer resolves is an error, not a miss. Reporting it
// as absent would let the caller treat the layer as unapplied and convert one
// into a path that is not there, silently committing an empty layer.
func (s *snapshotter) resolveBlob(id string, info snapshots.Info) (string, blobSource, error) {
	src, err := blobSourceFromInfo(info)
	if err != nil {
		return "", blobSource{}, err
	}
	if src.populated() {
		if _, err := os.Stat(src.Ref); err != nil {
			return "", src, fmt.Errorf("layer blob %q from the %s is unusable: %w", src.Ref, src.Kind, err)
		}
		return src.Ref, src, nil
	}

	layerBlob := s.layerBlobPath(id)
	if _, err := os.Stat(layerBlob); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", src, errNoLayerBlob
		}
		return "", src, fmt.Errorf("failed to stat layer blob %q: %w", layerBlob, err)
	}
	return layerBlob, src, nil
}
