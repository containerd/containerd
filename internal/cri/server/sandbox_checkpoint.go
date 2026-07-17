//go:build !linux

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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// CheckpointPod is not supported on non-Linux platforms.
func (c *criService) CheckpointPod(_ context.Context, _ *runtime.CheckpointPodRequest) (*runtime.CheckpointPodResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method CheckpointPod not implemented")
}

// RestorePod is not supported on non-Linux platforms.
func (c *criService) RestorePod(_ context.Context, _ *runtime.RestorePodRequest) (*runtime.RestorePodResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method RestorePod not implemented")
}
