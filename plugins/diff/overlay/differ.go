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

// Package overlay implements a differ that uses the overlayfs upperdir
// for efficient diff computation. Instead of walking both lower and upper
// filesystem trees, it reads only the changed files tracked by the overlay
// kernel module in the upperdir, which avoids scanning potentially large
// lower layers.
package overlay

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
)

var emptyDesc = ocispec.Descriptor{}

// overlayDiff implements diff.Comparer using overlayfs's native upperdir
// to compute diffs without scanning the entire lower filesystem.
type overlayDiff struct {
	store content.Store
}

// NewOverlayDiff creates a new Comparer that uses the overlay filesystem's
// upperdir for efficient diff computation. It only walks the upperdir instead
// of doing a double-walk of lower and upper directories, which is significantly
// faster for large images where only a small fraction of files have changed.
//
// The upper mounts must be overlay-backed. If they are not, Compare returns
// errdefs.ErrNotImplemented so that the caller can fall back to a generic differ.
func NewOverlayDiff(store content.Store) diff.Comparer {
	return &overlayDiff{store: store}
}

func uniqueRef() string {
	t := time.Now()
	var b [3]byte
	// Ignore read failures, just decreases uniqueness
	rand.Read(b[:])
	return fmt.Sprintf("%d-%s", t.UnixNano(), base64.URLEncoding.EncodeToString(b[:]))
}
