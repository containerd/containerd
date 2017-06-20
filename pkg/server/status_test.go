/*
Copyright 2017 The Kubernetes Authors.

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
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	healthapi "google.golang.org/grpc/health/grpc_health_v1"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"
)

func TestStatus(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	for desc, test := range map[string]struct {
		containerdCheckRes *healthapi.HealthCheckResponse
		containerdCheckErr error
		networkStatusErr   error

		expectRuntimeNotReady bool
		expectNetworkNotReady bool
	}{
		"runtime should not be ready when containerd is not serving": {
			containerdCheckRes: &healthapi.HealthCheckResponse{
				Status: healthapi.HealthCheckResponse_NOT_SERVING,
			},
			expectRuntimeNotReady: true,
		},
		"runtime should not be ready when containerd healthcheck returns error": {
			containerdCheckErr:    errors.New("healthcheck error"),
			expectRuntimeNotReady: true,
		},
		"network should not be ready when network plugin status returns error": {
			containerdCheckRes: &healthapi.HealthCheckResponse{
				Status: healthapi.HealthCheckResponse_SERVING,
			},
			networkStatusErr:      errors.New("status error"),
			expectNetworkNotReady: true,
		},
		"runtime should be ready when containerd is serving": {
			containerdCheckRes: &healthapi.HealthCheckResponse{
				Status: healthapi.HealthCheckResponse_SERVING,
			},
		},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		ctx := context.Background()
		mock := servertesting.NewMockHealthClient(ctrl)
		mock.EXPECT().Check(ctx, &healthapi.HealthCheckRequest{}).Return(
			test.containerdCheckRes, test.containerdCheckErr)
		c.healthService = mock
		if test.networkStatusErr != nil {
			c.netPlugin.(*servertesting.FakeCNIPlugin).InjectError(
				"Status", test.networkStatusErr)
		}

		resp, err := c.Status(ctx, &runtime.StatusRequest{})
		assert.NoError(t, err)
		require.NotNil(t, resp)
		runtimeCondition := resp.Status.Conditions[0]
		networkCondition := resp.Status.Conditions[1]
		assert.Equal(t, runtime.RuntimeReady, runtimeCondition.Type)
		assert.Equal(t, test.expectRuntimeNotReady, !runtimeCondition.Status)
		if test.expectRuntimeNotReady {
			assert.Equal(t, runtimeNotReadyReason, runtimeCondition.Reason)
			assert.NotEmpty(t, runtimeCondition.Message)
		}
		assert.Equal(t, runtime.NetworkReady, networkCondition.Type)
		assert.Equal(t, test.expectNetworkNotReady, !networkCondition.Status)
		if test.expectNetworkNotReady {
			assert.Equal(t, networkNotReadyReason, networkCondition.Reason)
			assert.NotEmpty(t, networkCondition.Message)
		}
	}
}
