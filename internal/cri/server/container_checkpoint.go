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
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func (c *criService) CheckpointContainer(ctx context.Context, r *runtime.CheckpointContainerRequest) (res *runtime.CheckpointContainerResponse, err error) {
	// The next line is just needed to make the linter happy.
	containerCheckpointTimer.WithValues("no-runtime").UpdateSince(time.Now())
	return nil, status.Errorf(codes.Unimplemented, "method CheckpointContainer not implemented")
}
