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

package sandbox

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	api "github.com/containerd/containerd/api/services/sandbox/v1"
	apitypes "github.com/containerd/containerd/api/types"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/events/exchange"
	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

type stubController struct {
	lastCreateSandbox sandbox.Sandbox
	lastCreateOpts    sandbox.CreateOptions
	lastStopID        string
	lastWaitID        string
	lastStatusID      string
	lastStatusVerbose bool
	statusExtra       *anypb.Any
	lastShutdownID    string
	lastMetricsID     string
	lastUpdateID      string
	lastUpdateSandbox sandbox.Sandbox
	lastUpdateFields  []string

	createErr   error
	startErr    error
	stopErr     error
	waitErr     error
	statusErr   error
	shutdownErr error
	metricsErr  error
	updateErr   error
}

var createdAt = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
var exitedAt = time.Date(2025, 1, 1, 0, 1, 0, 0, time.UTC)

func (s *stubController) Create(_ context.Context, sb sandbox.Sandbox, opts ...sandbox.CreateOpt) error {
	s.lastCreateSandbox = sb
	var co sandbox.CreateOptions
	for _, o := range opts {
		if err := o(&co); err != nil {
			return err
		}
	}
	s.lastCreateOpts = co
	return s.createErr
}

func (s *stubController) Start(_ context.Context, id string) (sandbox.ControllerInstance, error) {
	if s.startErr != nil {
		return sandbox.ControllerInstance{}, s.startErr
	}
	return sandbox.ControllerInstance{
		SandboxID: id,
		Pid:       42,
		CreatedAt: createdAt,
		Labels:    map[string]string{"env": "test"},
	}, nil
}

func (s *stubController) Platform(_ context.Context, _ string) (imagespec.Platform, error) {
	return imagespec.Platform{OS: "linux", Architecture: "amd64"}, nil
}

func (s *stubController) Stop(_ context.Context, id string, _ ...sandbox.StopOpt) error {
	s.lastStopID = id
	return s.stopErr
}

func (s *stubController) Wait(_ context.Context, id string) (sandbox.ExitStatus, error) {
	s.lastWaitID = id
	if s.waitErr != nil {
		return sandbox.ExitStatus{}, s.waitErr
	}
	return sandbox.ExitStatus{ExitStatus: 0, ExitedAt: exitedAt}, nil
}

func (s *stubController) Status(_ context.Context, id string, verbose bool) (sandbox.ControllerStatus, error) {
	s.lastStatusID = id
	s.lastStatusVerbose = verbose
	if s.statusErr != nil {
		return sandbox.ControllerStatus{}, s.statusErr
	}
	return sandbox.ControllerStatus{
		SandboxID: id,
		Pid:       42,
		State:     "ready",
		Info:      map[string]string{"version": "1"},
		CreatedAt: createdAt,
		ExitedAt:  exitedAt,
		Extra:     s.statusExtra,
	}, nil
}

func (s *stubController) Shutdown(_ context.Context, id string) error {
	s.lastShutdownID = id
	return s.shutdownErr
}

func (s *stubController) Metrics(_ context.Context, id string) (*apitypes.Metric, error) {
	s.lastMetricsID = id
	if s.metricsErr != nil {
		return nil, s.metricsErr
	}
	return &apitypes.Metric{}, nil
}

func (s *stubController) Update(_ context.Context, id string, sb sandbox.Sandbox, fields ...string) error {
	s.lastUpdateID = id
	s.lastUpdateSandbox = sb
	s.lastUpdateFields = fields
	return s.updateErr
}

func newTestService(t *testing.T) (*controllerService, *stubController) {
	t.Helper()
	ctrl := &stubController{}
	return &controllerService{
		sc:        map[string]sandbox.Controller{"test": ctrl},
		publisher: exchange.NewExchange(),
	}, ctrl
}

func nsCtx() context.Context {
	return namespaces.WithNamespace(context.Background(), "testing")
}

func TestGetController(t *testing.T) {
	tests := []struct {
		name      string
		sandboxer string
		wantCode  codes.Code
	}{
		{
			name:      "empty sandboxer returns InvalidArgument",
			sandboxer: "",
			wantCode:  codes.InvalidArgument,
		},
		{
			name:      "unknown sandboxer returns NotFound",
			sandboxer: "nonexistent",
			wantCode:  codes.NotFound,
		},
	}

	for _, tc := range tests {
		t.Run("Create/"+tc.name, func(t *testing.T) {
			svc, _ := newTestService(t)

			_, err := svc.Create(nsCtx(), &api.ControllerCreateRequest{
				SandboxID: "sb-1",
				Sandboxer: tc.sandboxer,
			})
			require.Error(t, err)
			s, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, tc.wantCode, s.Code())
		})

		t.Run("Start/"+tc.name, func(t *testing.T) {
			svc, _ := newTestService(t)

			_, err := svc.Start(nsCtx(), &api.ControllerStartRequest{
				SandboxID: "sb-1",
				Sandboxer: tc.sandboxer,
			})
			require.Error(t, err)
			s, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, tc.wantCode, s.Code())
		})
	}
}

func TestCreate(t *testing.T) {
	t.Run("delegates to controller and returns sandbox ID", func(t *testing.T) {
		svc, ctrl := newTestService(t)

		resp, err := svc.Create(nsCtx(), &api.ControllerCreateRequest{
			SandboxID: "sb-1",
			Sandboxer: "test",
			NetnsPath: "/var/run/netns/test",
			Rootfs: []*apitypes.Mount{
				{Type: "overlay", Source: "src", Options: []string{"ro"}},
			},
			Annotations: map[string]string{"io.containerd.netns": "/var/run/netns/test"},
		})
		require.NoError(t, err)
		assert.Equal(t, "sb-1", resp.SandboxID)
		assert.Equal(t, "sb-1", ctrl.lastCreateSandbox.ID)
		assert.Equal(t, "/var/run/netns/test", ctrl.lastCreateOpts.NetNSPath)
		require.Len(t, ctrl.lastCreateOpts.Rootfs, 1)
		assert.Equal(t, "overlay", ctrl.lastCreateOpts.Rootfs[0].Type)
		assert.Equal(t, "src", ctrl.lastCreateOpts.Rootfs[0].Source)
		assert.Equal(t, map[string]string{"io.containerd.netns": "/var/run/netns/test"}, ctrl.lastCreateOpts.Annotations)
	})

	t.Run("uses Sandbox proto when provided", func(t *testing.T) {
		svc, ctrl := newTestService(t)

		_, err := svc.Create(nsCtx(), &api.ControllerCreateRequest{
			SandboxID: "sb-2",
			Sandboxer: "test",
			Sandbox: &apitypes.Sandbox{
				SandboxID: "sb-2",
				Runtime:   &apitypes.Sandbox_Runtime{Name: "test-runtime"},
				Labels:    map[string]string{"key": "val"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "test-runtime", ctrl.lastCreateSandbox.Runtime.Name)
		assert.Equal(t, "val", ctrl.lastCreateSandbox.Labels["key"])
	})

	t.Run("propagates controller error", func(t *testing.T) {
		svc, ctrl := newTestService(t)
		ctrl.createErr = fmt.Errorf("disk full")

		_, err := svc.Create(nsCtx(), &api.ControllerCreateRequest{
			SandboxID: "sb-3",
			Sandboxer: "test",
		})
		require.Error(t, err)
	})
}

func TestStart(t *testing.T) {
	t.Run("returns instance fields from controller", func(t *testing.T) {
		svc, _ := newTestService(t)

		resp, err := svc.Start(nsCtx(), &api.ControllerStartRequest{
			SandboxID: "sb-1",
			Sandboxer: "test",
		})
		require.NoError(t, err)
		assert.Equal(t, "sb-1", resp.SandboxID)
		assert.Equal(t, uint32(42), resp.Pid)
		assert.Equal(t, "test", resp.Labels["env"])
	})
}

func TestStop(t *testing.T) {
	t.Run("delegates sandbox ID to controller", func(t *testing.T) {
		svc, ctrl := newTestService(t)

		_, err := svc.Stop(nsCtx(), &api.ControllerStopRequest{
			SandboxID: "sb-1",
			Sandboxer: "test",
		})
		require.NoError(t, err)
		assert.Equal(t, "sb-1", ctrl.lastStopID)
	})
}

func TestWait(t *testing.T) {
	t.Run("returns exit status from controller", func(t *testing.T) {
		svc, ctrl := newTestService(t)

		resp, err := svc.Wait(nsCtx(), &api.ControllerWaitRequest{
			SandboxID: "sb-1",
			Sandboxer: "test",
		})
		require.NoError(t, err)
		assert.Equal(t, uint32(0), resp.ExitStatus)
		assert.Equal(t, "sb-1", ctrl.lastWaitID)
	})
}

func TestStatus(t *testing.T) {
	t.Run("returns status fields from controller", func(t *testing.T) {
		svc, ctrl := newTestService(t)

		resp, err := svc.Status(nsCtx(), &api.ControllerStatusRequest{
			SandboxID: "sb-1",
			Sandboxer: "test",
			Verbose:   true,
		})
		require.NoError(t, err)
		assert.Equal(t, "sb-1", resp.SandboxID)
		assert.Equal(t, "ready", resp.State)
		assert.Equal(t, uint32(42), resp.Pid)
		assert.True(t, ctrl.lastStatusVerbose)
	})

	t.Run("returns non-nil Extra when controller Extra is nil", func(t *testing.T) {
		svc, _ := newTestService(t)

		resp, err := svc.Status(nsCtx(), &api.ControllerStatusRequest{
			SandboxID: "sb-1",
			Sandboxer: "test",
		})
		require.NoError(t, err)
		assert.NotNil(t, resp.Extra)
	})

	t.Run("copies Extra fields when controller Extra is non-nil", func(t *testing.T) {
		svc, ctrl := newTestService(t)
		ctrl.statusExtra = &anypb.Any{
			TypeUrl: "type.googleapis.com/test.Status",
			Value:   []byte("payload"),
		}

		resp, err := svc.Status(nsCtx(), &api.ControllerStatusRequest{
			SandboxID: "sb-1",
			Sandboxer: "test",
		})
		require.NoError(t, err)
		require.NotNil(t, resp.Extra)
		assert.Equal(t, "type.googleapis.com/test.Status", resp.Extra.TypeUrl)
		assert.Equal(t, []byte("payload"), resp.Extra.Value)
	})
}

func TestShutdown(t *testing.T) {
	t.Run("delegates to controller", func(t *testing.T) {
		svc, ctrl := newTestService(t)

		_, err := svc.Shutdown(nsCtx(), &api.ControllerShutdownRequest{
			SandboxID: "sb-1",
			Sandboxer: "test",
		})
		require.NoError(t, err)
		assert.Equal(t, "sb-1", ctrl.lastShutdownID)
	})
}

func TestMetrics(t *testing.T) {
	t.Run("delegates to controller", func(t *testing.T) {
		svc, ctrl := newTestService(t)

		resp, err := svc.Metrics(nsCtx(), &api.ControllerMetricsRequest{
			SandboxID: "sb-1",
			Sandboxer: "test",
		})
		require.NoError(t, err)
		assert.NotNil(t, resp.Metrics)
		assert.Equal(t, "sb-1", ctrl.lastMetricsID)
	})
}

func TestUpdate(t *testing.T) {
	t.Run("forwards sandbox and fields to controller", func(t *testing.T) {
		svc, ctrl := newTestService(t)

		_, err := svc.Update(nsCtx(), &api.ControllerUpdateRequest{
			SandboxID: "sb-1",
			Sandboxer: "test",
			Sandbox: &apitypes.Sandbox{
				SandboxID: "sb-1",
				Runtime:   &apitypes.Sandbox_Runtime{Name: "rt"},
				Labels:    map[string]string{"a": "b"},
			},
			Fields: []string{"labels"},
		})
		require.NoError(t, err)
		assert.Equal(t, "sb-1", ctrl.lastUpdateID)
		assert.Equal(t, []string{"labels"}, ctrl.lastUpdateFields)
		assert.Equal(t, "rt", ctrl.lastUpdateSandbox.Runtime.Name)
		assert.Equal(t, "b", ctrl.lastUpdateSandbox.Labels["a"])
	})

	t.Run("returns error when sandbox is nil", func(t *testing.T) {
		svc, _ := newTestService(t)

		_, err := svc.Update(nsCtx(), &api.ControllerUpdateRequest{
			SandboxID: "sb-1",
			Sandboxer: "test",
		})
		require.Error(t, err)
	})
}
