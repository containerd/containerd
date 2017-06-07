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

	versionapi "github.com/containerd/containerd/api/services/version"
	"github.com/golang/mock/gomock"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	servertesting "github.com/kubernetes-incubator/cri-containerd/pkg/server/testing"
)

func TestVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	// TODO(random-liu): Check containerd version after containerd fixes its version.
	for desc, test := range map[string]struct {
		versionRes      *versionapi.VersionResponse
		versionErr      error
		expectErr       bool
		expectedVersion string
	}{
		"should return error if containerd version returns error": {
			versionErr: errors.New("random error"),
			expectErr:  true,
		},
		"should not return error if containerd version returns successfully": {
			versionRes: &versionapi.VersionResponse{Version: "1.1.1"},
			expectErr:  false,
		},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		ctx := context.Background()
		mock := servertesting.NewMockVersionClient(ctrl)
		mock.EXPECT().Version(ctx, &empty.Empty{}).Return(test.versionRes, test.versionErr)

		c.versionService = mock
		v, err := c.Version(ctx, &runtime.VersionRequest{})
		if test.expectErr {
			assert.Equal(t, test.expectErr, err != nil)
		} else {
			assert.Equal(t, test.versionRes.Version, v.RuntimeVersion)
		}
	}
}
