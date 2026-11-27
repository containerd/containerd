//go:build windows

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

package windows

import (
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
)

func TestMountPairToLayerStackEmptyLower(t *testing.T) {
	upper := []mount.Mount{{
		Type:   "windows-layer",
		Source: `C:\layers\upper`,
	}}

	layers, err := mountPairToLayerStack(nil, upper)
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 1 || layers[0] != upper[0].Source {
		t.Fatalf("unexpected layer stack: %v", layers)
	}
}

func TestMountPairToLayerStackEmptyLowerWithReadOnlyUpperView(t *testing.T) {
	upper := []mount.Mount{{
		Type:   "windows-layer",
		Source: `C:\layers\temporary-view`,
		Options: []string{
			"ro",
			`parentLayerPaths=["C:\\layers\\base"]`,
		},
	}}

	layers, err := mountPairToLayerStack(nil, upper)
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 1 || layers[0] != `C:\layers\base` {
		t.Fatalf("unexpected layer stack: %v", layers)
	}
}

func TestMountPairToLayerStackEmptyLowerRejectsParentedUpper(t *testing.T) {
	upper := []mount.Mount{{
		Type:   "windows-layer",
		Source: `C:\layers\upper`,
		Options: []string{
			`parentLayerPaths=["C:\\layers\\parent"]`,
		},
	}}

	if _, err := mountPairToLayerStack(nil, upper); err == nil {
		t.Fatal("expected parented upper layer to be rejected")
	}
}

func TestMountPairToLayerStackEmptyLowerRejectsNullUpper(t *testing.T) {
	upper := []mount.Mount{{
		Type:    "windows-layer",
		Source:  `C:\layers\empty-view`,
		Options: []string{"ro"},
	}}

	if _, err := mountPairToLayerStack(nil, upper); err == nil {
		t.Fatal("expected null upper layer to be rejected")
	}
}
