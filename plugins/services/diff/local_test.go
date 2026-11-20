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

package diff

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	corediff "github.com/containerd/containerd/v2/core/diff"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type testDiffer struct{}

func (*testDiffer) Compare(context.Context, []mount.Mount, []mount.Mount, ...corediff.Opt) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, nil
}

func (*testDiffer) Apply(context.Context, ocispec.Descriptor, []mount.Mount, ...corediff.ApplyOpt) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, nil
}

func TestOrderDiffersOptionalOverlay(t *testing.T) {
	walking := &testDiffer{}
	overlay := &testDiffer{}
	tests := []struct {
		name string
		available map[string]interface{}
		order []string
		want []differ
		wantError bool
	}{
		{
			name: "overlay unavailable but walking fallback available",
			available: map[string]interface{}{"walking": walking},
			order: []string{"overlay", "walking"},
			want: []differ{walking},
		},
		{
			name: "both configured differs loaded",
			available: map[string]interface{}{"overlay": overlay, "walking": walking},
			order: []string{"overlay", "walking"},
			want: []differ{overlay, walking},
		},
		{
			name: "walking unavailable",
			available: map[string]interface{}{"overlay": overlay},
			order: []string{"overlay", "walking"},
			wantError: true,
		},
		{
			name: "overlay unavailable without fallback",
			available: map[string]interface{}{"walking": walking},
			order: []string{"overlay"},
			wantError: true,
		},
		{
			name: "wrong overlay implementation",
			available: map[string]interface{}{"overlay": struct{}{}, "walking": walking},
			order: []string{"overlay", "walking"},
			wantError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := orderDiffers(tt.available, tt.order)
			if (err != nil) != tt.wantError {
				t.Fatalf("orderDiffers() error = %v, wantError = %v", err, tt.wantError)
			}
			if tt.wantError {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("orderDiffers() len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("orderDiffers()[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
