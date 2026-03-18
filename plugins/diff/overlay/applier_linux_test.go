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
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
)

func TestGetOverlayPath(t *testing.T) {
	good := []string{"upperdir=/test/upper", "lowerdir=/test/lower1:/test/lower2", "workdir=/test/work"}
	path, parents, err := getOverlayPath(good)
	if err != nil {
		t.Fatalf("Get overlay path failed: %v", err)
	}
	if path != "/test/upper" {
		t.Fatalf("Unexpected upperdir: %q", path)
	}
	if len(parents) != 2 || parents[0] != "/test/lower1" || parents[1] != "/test/lower2" {
		t.Fatalf("Unexpected parents: %v", parents)
	}

	bad := []string{"lowerdir=/test/lower"}
	_, _, err = getOverlayPath(bad)
	if err == nil {
		t.Fatalf("An error is expected")
	}
}

func TestInvalidParameters(t *testing.T) {
	ctx := t.Context()

	testcases := []struct {
		name   string
		mounts []mount.Mount
	}{
		{
			name:   "empty mounts",
			mounts: []mount.Mount{},
		},
		{
			name: "multiple mounts",
			mounts: []mount.Mount{
				{Type: "overlay"},
				{Type: "overlay"},
			},
		},
		{
			name: "unsupported mount type",
			mounts: []mount.Mount{
				{
					Type: "unsupported",
				},
			},
		},
		{
			name: "invalid overlay options",
			mounts: []mount.Mount{
				{
					Type:    "overlay",
					Options: []string{"lowerdir=/test/lower"},
				},
			},
		},
		{
			name: "invalid bind mount",
			mounts: []mount.Mount{
				{
					Type: "bind",
				},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			err := Apply(ctx, tc.mounts, nil, false)
			if err == nil {
				t.Fatalf("An error is expected")
			}
		})
	}
}
