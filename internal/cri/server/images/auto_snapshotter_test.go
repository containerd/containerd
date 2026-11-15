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

package images

import (
	"context"
	"testing"

	transfertypes "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/platforms"
	"github.com/containerd/typeurl/v2"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"

	"github.com/containerd/containerd/v2/core/snapshots"
	transferimage "github.com/containerd/containerd/v2/core/transfer/image"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	crilabels "github.com/containerd/containerd/v2/internal/cri/labels"
)

func TestSnapshotterForImage(t *testing.T) {
	erofsLabel := map[string]string{crilabels.ImageSnapshotterLabelKey: "erofs"}
	for _, tc := range []struct {
		name    string
		auto    bool
		runtime criconfig.Runtime
		labels  map[string]string
		want    string
	}{
		{name: "default without label", auto: true, want: "overlayfs"},
		{name: "label selects erofs", auto: true, labels: erofsLabel, want: "erofs"},
		{name: "runtime snapshotter wins", auto: true, runtime: criconfig.Runtime{Snapshotter: "native"}, labels: erofsLabel, want: "native"},
		{name: "label ignored when auto is off", labels: erofsLabel, want: "overlayfs"},
		{name: "unknown snapshotter in label", auto: true, labels: map[string]string{crilabels.ImageSnapshotterLabelKey: "zfs"}, want: "overlayfs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestCRIService()
			c.config.Snapshotter = "overlayfs"
			c.autoSnapshotter = tc.auto
			c.snapshotters = map[string]snapshots.Snapshotter{"overlayfs": nil, "erofs": nil}
			assert.Equal(t, tc.want, c.SnapshotterForImage(context.Background(), tc.runtime, tc.labels))
		})
	}
}

func TestImageSnapshotters(t *testing.T) {
	c, _ := newTestCRIService()
	c.config.Snapshotter = "overlayfs"
	c.snapshotters = map[string]snapshots.Snapshotter{"overlayfs": nil, "erofs": nil}

	assert.Equal(t, []string{"overlayfs"}, c.ImageSnapshotters(context.Background(), criconfig.Runtime{}))

	c.autoSnapshotter = true
	assert.Equal(t, []string{"overlayfs", "erofs"}, c.ImageSnapshotters(context.Background(), criconfig.Runtime{}))
	assert.Equal(t, []string{"native"}, c.ImageSnapshotters(context.Background(), criconfig.Runtime{Snapshotter: "native"}))
}

func TestResolveRuntimeHandlerSnapshotterSet(t *testing.T) {
	c, _ := newTestCRIService()
	c.config.Snapshotter = "overlayfs"
	c.runtimePlatforms["default"] = &ImagePlatform{}
	c.runtimePlatforms["pinned"] = &ImagePlatform{Snapshotter: "overlayfs"}

	h, err := c.resolveRuntimeHandler("default")
	assert.NoError(t, err)
	assert.Equal(t, "overlayfs", h.Snapshotter)
	assert.False(t, h.SnapshotterSet)

	h, err = c.resolveRuntimeHandler("pinned")
	assert.NoError(t, err)
	assert.Equal(t, "overlayfs", h.Snapshotter)
	assert.True(t, h.SnapshotterSet, "a handler that sets the default snapshotter explicitly still pins it")
}

func TestAutoSnapshotterStoreOpts(t *testing.T) {
	p := imagespec.Platform{OS: "linux", Architecture: "amd64"}
	ep := imagespec.Platform{OS: "linux", Architecture: "amd64", OSFeatures: []string{"erofs"}}

	s := transferimage.NewStore("example.com/test:latest", autoSnapshotterStoreOpts(p, "overlayfs")...)
	a, err := s.MarshalAny(context.Background(), nil)
	assert.NoError(t, err)
	var wire transfertypes.ImageStore
	assert.NoError(t, typeurl.UnmarshalTo(a, &wire))
	assert.Equal(t, uint32(1), wire.ManifestLimit, "only the preferred variant must be fetched")
	unpacks := s.UnpackPlatforms()
	if assert.Len(t, unpacks, 2) {
		assert.Equal(t, p, unpacks[0].Platform)
		assert.Equal(t, "overlayfs", unpacks[0].Snapshotter)
		assert.Equal(t, ep, unpacks[1].Platform)
		assert.Equal(t, "erofs", unpacks[1].Snapshotter)
	}

	m := autoSnapshotterMatcher(p)
	assert.True(t, m.Match(p))
	assert.True(t, m.Match(ep))
	assert.True(t, m.Less(ep, p), "the EROFS variant must be preferred")
	assert.False(t, m.Match(platforms.MustParse("linux/arm64")))
	assert.Empty(t, p.OSFeatures, "the input platform must not be modified")
}
