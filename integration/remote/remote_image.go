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

package remote

import (
	"context"
	"time"

	internalapi "github.com/containerd/containerd/v2/integration/cri-api/pkg/apis"
	"google.golang.org/grpc"
	upstreamapi "k8s.io/cri-api/pkg/apis"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	upstreamcri "k8s.io/cri-client/pkg"
)

// ImageService adapts the upstream CRI image client to the legacy integration
// interface used by containerd's integration tests.
type ImageService struct {
	imageService upstreamapi.ImageManagerService
}

var _ internalapi.ImageManagerService = (*ImageService)(nil)

// NewImageService creates a new internalapi.ImageManagerService.
func NewImageService(endpoint string, connectionTimeout time.Duration) (internalapi.ImageManagerService, error) {
	imageService, err := upstreamcri.NewRemoteImageService(context.Background(), endpoint, connectionTimeout, nil, false)
	if err != nil {
		return nil, err
	}

	return &ImageService{imageService: imageService}, nil
}

func (r *ImageService) ListImages(filter *runtimeapi.ImageFilter, _ ...grpc.CallOption) ([]*runtimeapi.Image, error) {
	return r.imageService.ListImages(context.Background(), filter)
}

func (r *ImageService) ImageStatus(image *runtimeapi.ImageSpec, _ ...grpc.CallOption) (*runtimeapi.Image, error) {
	resp, err := r.imageService.ImageStatus(context.Background(), image, false)
	if err != nil {
		return nil, err
	}
	return resp.GetImage(), nil
}

func (r *ImageService) PullImage(image *runtimeapi.ImageSpec, auth *runtimeapi.AuthConfig, podSandboxConfig *runtimeapi.PodSandboxConfig, runtimeHandler string, _ ...grpc.CallOption) (string, error) {
	requestImage := image
	if image != nil && runtimeHandler != "" {
		copied := *image
		copied.RuntimeHandler = runtimeHandler
		requestImage = &copied
	}

	return r.imageService.PullImage(context.Background(), requestImage, auth, podSandboxConfig)
}

func (r *ImageService) RemoveImage(image *runtimeapi.ImageSpec, _ ...grpc.CallOption) error {
	return r.imageService.RemoveImage(context.Background(), image)
}

func (r *ImageService) ImageFsInfo(_ ...grpc.CallOption) ([]*runtimeapi.FilesystemUsage, error) {
	resp, err := r.imageService.ImageFsInfo(context.Background())
	if err != nil {
		return nil, err
	}
	return resp.GetImageFilesystems(), nil
}
