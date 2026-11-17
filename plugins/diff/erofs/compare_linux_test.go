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
	"context"
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/plugins/content/local"
)

// TestCompareFailedWriteReleasesRef checks that a Compare whose diff write
// fails releases the content writer, so the caller can reopen its reference.
func TestCompareFailedWriteReleasesRef(t *testing.T) {
	for _, mediaType := range []string{ocispec.MediaTypeImageLayer, ocispec.MediaTypeImageLayerGzip} {
		t.Run(mediaType, func(t *testing.T) {
			ctx := context.Background()
			cs, err := local.NewStore(t.TempDir())
			require.NoError(t, err)

			snapshot := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(snapshot, ".erofslayer"), nil, 0644))
			require.NoError(t, os.Mkdir(filepath.Join(snapshot, "fs"), 0755))
			upper := []mount.Mount{{Type: "bind", Source: filepath.Join(snapshot, "fs"), Options: []string{"rw", "rbind"}}}
			// No kernel filesystem has this type, so mounting lower fails.
			lower := []mount.Mount{{Type: "containerd-test-invalid", Source: t.TempDir()}}

			const ref = "compare-failed-write"
			_, err = NewErofsDiffer(cs).Compare(ctx, lower, upper, diff.WithMediaType(mediaType), diff.WithReference(ref))
			require.Error(t, err, "Compare with an unmountable lower")

			w, err := cs.Writer(ctx, content.WithRef(ref))
			require.NoError(t, err, "reopen writer for ref %q after failed Compare", ref)
			require.NoError(t, w.Close())
		})
	}
}
