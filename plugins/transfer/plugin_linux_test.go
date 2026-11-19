//go:build linux

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

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/transfer/local"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/platforms"
	"github.com/containerd/plugin"
)

func TestConfigureUnpackPlatformsDefaultConfigSkipsUnavailableErofsDiffer(t *testing.T) {
	defaultSpec := platforms.DefaultSpec()
	erofsSpec := defaultSpec
	erofsSpec.OSFeatures = []string{"erofs"}

	ms, ic := newTestInitContext(t, map[string]snapshots.Snapshotter{
		defaults.DefaultSnapshotter: &testSnapshotter{},
		"erofs":                     &testSnapshotter{},
	}, []*plugin.Registration{
		newTestDiffPlugin(defaults.DefaultDiffer, testApplier{}, nil, defaultSpec),
		newTestDiffPlugin("erofs", nil, plugin.ErrSkipPlugin, erofsSpec),
	})
	lc := &local.TransferConfig{}

	err := configureUnpackPlatforms(ic, ms, defaultConfig(), lc)
	if err != nil {
		t.Fatal(err)
	}
	if len(lc.UnpackPlatforms) != 1 {
		t.Fatalf("expected only default unpack platform, got %d", len(lc.UnpackPlatforms))
	}
	if lc.UnpackPlatforms[0].SnapshotterKey != defaults.DefaultSnapshotter {
		t.Fatalf("expected default snapshotter, got %q", lc.UnpackPlatforms[0].SnapshotterKey)
	}
}
