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

	"github.com/stretchr/testify/assert"

	containerd "github.com/containerd/containerd/v2/client"
	criconfig "github.com/containerd/containerd/v2/pkg/cri/config"
	"github.com/containerd/containerd/v2/pkg/cri/server/podsandbox/types"
	sandboxstore "github.com/containerd/containerd/v2/pkg/cri/store/sandbox"
	ostesting "github.com/containerd/containerd/v2/pkg/os/testing"
)

const (
	testRootDir  = "/test/root"
	testStateDir = "/test/state"
)

var testConfig = criconfig.Config{
	RootDir:  testRootDir,
	StateDir: testStateDir,
	PluginConfig: criconfig.PluginConfig{
		TolerateMissingHugetlbController: true,
	},
}

// newControllerService creates a fake criService for test.
func newControllerService() *Controller {
	return &Controller{
		config: testConfig,
		os:     ostesting.NewFakeOS(),
		store:  NewStore(),
	}
}

func Test_Status(t *testing.T) {
	sandboxID, pid, exitStatus := "1", uint32(1), uint32(0)
	createdAt, exitedAt := time.Now(), time.Now()
	controller := newControllerService()

	sb := types.NewPodSandbox(sandboxID, sandboxstore.Status{
		State:     sandboxstore.StateReady,
		Pid:       pid,
		CreatedAt: createdAt,
	})
	sb.Metadata = sandboxstore.Metadata{ID: sandboxID}
	err := controller.store.Save(sb)
	if err != nil {
		t.Fatal(err)
	}
	s, err := controller.Status(context.Background(), sandboxID, false)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, s.Pid, pid)
	assert.Equal(t, s.CreatedAt, createdAt)
	assert.Equal(t, s.State, sandboxstore.StateReady.String())

	sb.Exit(*containerd.NewExitStatus(exitStatus, exitedAt, nil))
	exit, err := controller.Wait(context.Background(), sandboxID)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, exit.ExitStatus, exitStatus)
	assert.Equal(t, exit.ExitedAt, exitedAt)

	s, err = controller.Status(context.Background(), sandboxID, false)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, s.State, sandboxstore.StateNotReady.String())
}
