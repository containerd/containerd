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

package transfer

import (
	"testing"

	"github.com/containerd/containerd/v2/core/transfer/local"
	"github.com/containerd/containerd/v2/core/unpack"
)

func TestValidateConfig(t *testing.T) {
	testcases := []struct {
		name     string
		config   *local.TransferConfig
		warnings int
	}{
		{
			name: "valid parallel",
			config: &local.TransferConfig{
				MaxConcurrentUnpacks: 2,
				UnpackPlatforms: []unpack.Platform{
					{
						SnapshotterCapabilities: []string{"rebase"},
						ApplierID:               "overlay",
					},
					{
						SnapshotterCapabilities: []string{"rebase"},
						ApplierID:               "erofs",
					},
				},
			},
			warnings: 0,
		},
		{
			name: "invalid parallel",
			config: &local.TransferConfig{
				MaxConcurrentUnpacks: 2,
				UnpackPlatforms: []unpack.Platform{
					{
						SnapshotterCapabilities: []string{"rebase"},
						ApplierID:               "walking",
					},
				},
			},
			warnings: 1,
		},
		{
			name: "unsupported snapshotter",
			config: &local.TransferConfig{
				MaxConcurrentUnpacks: 2,
				UnpackPlatforms: []unpack.Platform{
					{
						ApplierID: "overlay",
					},
					{
						ApplierID: "walking",
					},
				},
			},
			warnings: 0,
		},
		{
			name: "serial unpack",
			config: &local.TransferConfig{
				MaxConcurrentUnpacks: 1,
				UnpackPlatforms: []unpack.Platform{
					{
						SnapshotterCapabilities: []string{"rebase"},
						ApplierID:               "overlay",
					},
					{
						SnapshotterCapabilities: []string{"rebase"},
						ApplierID:               "walking",
					},
					{
						ApplierID: "overlay",
					},
					{
						ApplierID: "walking",
					},
				},
			},
			warnings: 0,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			warnings, err := validateConfig(tc.config)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(warnings) != tc.warnings {
				t.Errorf("expected %d warnings, got %d", tc.warnings, len(warnings))
			}
		})
	}
}
