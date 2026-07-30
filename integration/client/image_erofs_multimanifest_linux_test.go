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

package client

import (
	"testing"

	. "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/defaults"
	imagelist "github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/platforms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPullOnlyErofsImage pulls an only-erofs image on a non-erofs daemon:
// platforms.Default() rejects the erofs-tagged manifest, so the pull fails.
func TestPullOnlyErofsImage(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	cases := []struct {
		name  string
		image int
	}{
		{"raw", imagelist.ErofsOnlyRaw},
		{"zstd", imagelist.ErofsOnlyZstd},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ref := imagelist.Get(tc.image)

			ctx, cancel := testContext(t)
			defer cancel()

			client, err := newClient(t, address)
			require.NoError(t, err)
			defer client.Close()

			defer func() {
				_ = client.ImageService().Delete(ctx, ref, images.SynchronousDelete())
			}()

			_, err = client.Pull(ctx, ref,
				WithPlatformMatcher(platforms.Default()),
				WithPullUnpack,
			)
			assert.Errorf(t, err, "pull of only-erofs image %s should fail on non-erofs daemon", ref)
		})
	}
}

// TestPullMixedOciErofsImage pulls a mixed OCI+erofs index on a non-erofs
// daemon: the OCI manifest is the only candidate and unpack succeeds.
func TestPullMixedOciErofsImage(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	cases := []struct {
		name  string
		image int
	}{
		{"raw", imagelist.ErofsOciAndRaw},
		{"zstd", imagelist.ErofsOciAndZstd},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ref := imagelist.Get(tc.image)

			ctx, cancel := testContext(t)
			defer cancel()

			client, err := newClient(t, address)
			require.NoError(t, err)
			defer client.Close()

			defer func() {
				_ = client.ImageService().Delete(ctx, ref, images.SynchronousDelete())
			}()

			img, err := client.Pull(ctx, ref,
				WithPlatformMatcher(platforms.Default()),
				WithPullUnpack,
			)
			require.NoErrorf(t, err, "pull mixed oci+erofs image %s", ref)

			unpacked, err := img.IsUnpacked(ctx, defaults.DefaultSnapshotter)
			require.NoError(t, err)
			assert.True(t, unpacked, "expected mixed image %s to be unpacked via OCI fallback", ref)
		})
	}
}
