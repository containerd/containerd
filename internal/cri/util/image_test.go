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

package util

import (
	"testing"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	crilabels "github.com/containerd/containerd/v2/internal/cri/labels"
)

func TestImageForSnapshotter(t *testing.T) {
	client := &containerd.Client{}
	for _, tc := range []struct {
		name        string
		labels      map[string]string
		snapshotter string
		rebound     bool
		wantErr     error
	}{
		{name: "no labels", snapshotter: "overlayfs"},
		{
			name:        "runtime platform without snapshotter label",
			labels:      map[string]string{crilabels.ImagePlatformLabelKey: "linux/arm64"},
			snapshotter: "overlayfs",
		},
		{
			name: "erofs image used with erofs",
			labels: map[string]string{
				crilabels.ImagePlatformLabelKey:    "linux(+erofs)/amd64",
				crilabels.ImageSnapshotterLabelKey: "erofs",
			},
			snapshotter: "erofs",
			rebound:     true,
		},
		{
			name: "erofs image used with another snapshotter",
			labels: map[string]string{
				crilabels.ImagePlatformLabelKey:    "linux(+erofs)/amd64",
				crilabels.ImageSnapshotterLabelKey: "erofs",
			},
			snapshotter: "native",
			wantErr:     errdefs.ErrFailedPrecondition,
		},
		{
			name: "plain image used with another snapshotter",
			labels: map[string]string{
				crilabels.ImageSnapshotterLabelKey: "overlayfs",
			},
			snapshotter: "native",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := containerd.NewImage(client, images.Image{Name: "example.com/test:latest", Labels: tc.labels})
			out, err := ImageForSnapshotter(client, in, tc.snapshotter)
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
				return
			}
			assert.NoError(t, err)
			if tc.rebound {
				assert.NotEqual(t, in, out)
				assert.Equal(t, in.Metadata(), out.Metadata())
			} else {
				assert.Equal(t, in, out)
			}
		})
	}
}
