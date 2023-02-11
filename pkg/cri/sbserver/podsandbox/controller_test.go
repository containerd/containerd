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

package podsandbox

import (
	"context"
	"testing"
	"time"

	criconfig "github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/pkg/cri/store/label"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	ostesting "github.com/containerd/containerd/pkg/os/testing"
	"github.com/stretchr/testify/assert"
)

const (
	testRootDir  = "/test/root"
	testStateDir = "/test/state"
	// Use an image id as test sandbox image to avoid image name resolve.
	// TODO(random-liu): Change this to image name after we have complete image
	// management unit test framework.
	testSandboxImage = "sha256:c75bebcdd211f41b3a460c7bf82970ed6c75acaab9cd4c9a4e125b03ca113798"
)

var testConfig = criconfig.Config{
	RootDir:  testRootDir,
	StateDir: testStateDir,
	PluginConfig: criconfig.PluginConfig{
		SandboxImage:                     testSandboxImage,
		TolerateMissingHugetlbController: true,
	},
}

// newControllerService creates a fake criService for test.
func newControllerService() *Controller {
	labels := label.NewStore()
	return &Controller{
		config:       testConfig,
		os:           ostesting.NewFakeOS(),
		sandboxStore: sandboxstore.NewStore(labels),
	}
}

func Test_Status(t *testing.T) {
	sandboxID, pid, exitStatus := "1", uint32(1), uint32(0)
	createdAt, exitedAt := time.Now(), time.Now()
	controller := newControllerService()
	status := sandboxstore.Status{
		Pid:        pid,
		CreatedAt:  createdAt,
		ExitStatus: exitStatus,
		ExitedAt:   exitedAt,
		State:      sandboxstore.StateReady,
	}
	sb := sandboxstore.Sandbox{
		Metadata: sandboxstore.Metadata{
			ID: sandboxID,
		},
		Status: sandboxstore.StoreStatus(status),
	}
	err := controller.sandboxStore.Add(sb)
	if err != nil {
		t.Fatal(err)
	}
	s, err := controller.Status(context.Background(), sandboxID, false)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, s.Pid, pid)
	assert.Equal(t, s.ExitedAt, exitedAt)
	assert.Equal(t, s.State, sandboxstore.StateReady.String())
}
