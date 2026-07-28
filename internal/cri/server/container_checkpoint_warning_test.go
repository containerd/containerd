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

package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/core/sandbox"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/pkg/deprecation"
	"github.com/containerd/containerd/v2/plugins/services/warning"
)

type mockWarningService struct {
	emitted []deprecation.Warning
}

func (m *mockWarningService) Emit(ctx context.Context, w deprecation.Warning) {
	m.emitted = append(m.emitted, w)
}

func (m *mockWarningService) Warnings() []warning.Warning {
	return nil
}

type testSandboxService struct {
	fakeSandboxService
}

func (t *testSandboxService) SandboxStatus(ctx context.Context, sandboxer string, sandboxID string, verbose bool) (sandbox.ControllerStatus, error) {
	return sandbox.ControllerStatus{
		SandboxID: sandboxID,
		Pid:       1234,
		State:     "READY",
	}, nil
}

func TestCreateContainerCheckpointWarning(t *testing.T) {
	c := newTestCRIService()
	mockWarn := &mockWarningService{}
	c.warningService = mockWarn
	c.sandboxService = &testSandboxService{}

	sb := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:   "test-sandbox",
			Name: "test-sandbox",
			Config: &runtime.PodSandboxConfig{
				Metadata: &runtime.PodSandboxMetadata{Name: "test-sandbox", Namespace: "default"},
			},
		},
		sandboxstore.Status{
			State: sandboxstore.StateReady,
		},
	)
	require.NoError(t, c.sandboxStore.Add(sb))

	// In newTestCRIService(), c.os is a FakeOS where Stat returns (nil, nil) (no error),
	// causing checkpointImage to evaluate as true when checked in CreateContainer.
	_, _ = c.CreateContainer(context.Background(), &runtime.CreateContainerRequest{
		PodSandboxId: "test-sandbox",
		Config: &runtime.ContainerConfig{
			Metadata: &runtime.ContainerMetadata{Name: "test-container"},
			Image:    &runtime.ImageSpec{Image: "/path/to/checkpoint.tar"},
		},
		SandboxConfig: &runtime.PodSandboxConfig{
			Metadata: &runtime.PodSandboxMetadata{Name: "test-sandbox", Namespace: "default"},
		},
	})

	assert.Contains(t, mockWarn.emitted, deprecation.CRICreateContainerCheckpointRestore, "expected CRICreateContainerCheckpointRestore deprecation warning to be emitted")
}
