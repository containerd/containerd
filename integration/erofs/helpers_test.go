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

// helpers_test.go provides cross-platform test helpers used by daemon-based
// integration tests. These helpers have no platform-specific imports.
package erofs

import (
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/platforms"
	godigest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

const erofsSnapshotterName = "erofs"

// erofsPM returns a platform matcher for the local architecture with
// os.features=["erofs"], as required for selecting an EROFS manifest from
// a dual-format index.
func erofsPM() platforms.MatchComparer {
	spec := platforms.DefaultSpec()
	spec.OSFeatures = []string{"erofs"}
	return platforms.OnlyStrict(spec)
}

// chainIDs derives the OCI chain IDs for each layer in the manifest. The
// diff ID is obtained via images.GetDiffID, which reads the
// labels.LabelUncompressed content label from the content store (set during
// conversion by converter/erofs) or decompresses the layer on-the-fly if
// the label is absent.
//
// This function has no Linux-only dependencies.
func chainIDs(t *testing.T, c *containerd.Client, layers []ocispec.Descriptor) []string {
	t.Helper()
	ctx, cancel := testContext(t)
	defer cancel()

	var (
		chain []godigest.Digest
		ids   []string
	)
	for _, l := range layers {
		diffID, err := images.GetDiffID(ctx, c.ContentStore(), l)
		require.NoError(t, err, "compute diff ID for layer %s", l.Digest)
		chain = append(chain, diffID)
		ids = append(ids, identity.ChainID(chain).String())
	}
	return ids
}
