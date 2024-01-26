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

/*
Copyright 2016 The Kubernetes Authors.

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
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/klog/v2"

	internalapi "github.com/containerd/containerd/v2/integration/cri-api/pkg/apis"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/integration/remote/util"
)

// ImageService is a gRPC implementation of internalapi.ImageManagerService.
type ImageService struct {
	timeout     time.Duration
	imageClient runtimeapi.ImageServiceClient
}

// NewImageService creates a new internalapi.ImageManagerService.
func NewImageService(endpoint string, connectionTimeout time.Duration) (internalapi.ImageManagerService, error) {
	klog.V(3).Infof("Connecting to image service %s", endpoint)
	addr, dialer, err := util.GetAddressAndDialer(endpoint)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMsgSize)),
	)
	if err != nil {
		klog.Errorf("Connect remote image service %s failed: %v", addr, err)
		return nil, err
	}

	return &ImageService{
		timeout:     connectionTimeout,
		imageClient: runtimeapi.NewImageServiceClient(conn),
	}, nil
}

// ListImages lists available images.
func (r *ImageService) ListImages(filter *runtimeapi.ImageFilter, opts ...grpc.CallOption) ([]*runtimeapi.Image, error) {
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	resp, err := r.imageClient.ListImages(ctx, &runtimeapi.ListImagesRequest{
		Filter: filter,
	}, opts...)
	if err != nil {
		klog.Errorf("ListImages with filter %+v from image service failed: %v", filter, err)
		return nil, err
	}

	return resp.Images, nil
}

// ImageStatus returns the status of the image.
func (r *ImageService) ImageStatus(image *runtimeapi.ImageSpec, opts ...grpc.CallOption) (*runtimeapi.Image, error) {
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	resp, err := r.imageClient.ImageStatus(ctx, &runtimeapi.ImageStatusRequest{
		Image: image,
	}, opts...)
	if err != nil {
		klog.Errorf("ImageStatus %q from image service failed: %v", image.Image, err)
		return nil, err
	}

	if resp.Image != nil {
		if resp.Image.Id == "" || resp.Image.Size() == 0 {
			errorMessage := fmt.Sprintf("Id or size of image %q is not set", image.Image)
			klog.Errorf("ImageStatus failed: %s", errorMessage)
			return nil, errors.New(errorMessage)
		}
	}

	return resp.Image, nil
}

// PullImage pulls an image with authentication config.
func (r *ImageService) PullImage(image *runtimeapi.ImageSpec, auth *runtimeapi.AuthConfig, podSandboxConfig *runtimeapi.PodSandboxConfig, opts ...grpc.CallOption) (string, error) {
	ctx, cancel := getContextWithCancel()
	defer cancel()

	resp, err := r.imageClient.PullImage(ctx, &runtimeapi.PullImageRequest{
		Image:         image,
		Auth:          auth,
		SandboxConfig: podSandboxConfig,
	}, opts...)
	if err != nil {
		klog.Errorf("PullImage %q from image service failed: %v", image.Image, err)
		return "", err
	}

	if resp.ImageRef == "" {
		errorMessage := fmt.Sprintf("imageRef of image %q is not set", image.Image)
		klog.Errorf("PullImage failed: %s", errorMessage)
		return "", errors.New(errorMessage)
	}

	return resp.ImageRef, nil
}

// RemoveImage removes the image.
func (r *ImageService) RemoveImage(image *runtimeapi.ImageSpec, opts ...grpc.CallOption) error {
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	_, err := r.imageClient.RemoveImage(ctx, &runtimeapi.RemoveImageRequest{
		Image: image,
	}, opts...)
	if err != nil {
		klog.Errorf("RemoveImage %q from image service failed: %v", image.Image, err)
		return err
	}

	return nil
}

// ImageFsInfo returns information of the filesystem that is used to store images.
func (r *ImageService) ImageFsInfo(opts ...grpc.CallOption) ([]*runtimeapi.FilesystemUsage, error) {
	// Do not set timeout, because `ImageFsInfo` takes time.
	// TODO(random-liu): Should we assume runtime should cache the result, and set timeout here?
	ctx, cancel := getContextWithCancel()
	defer cancel()

	resp, err := r.imageClient.ImageFsInfo(ctx, &runtimeapi.ImageFsInfoRequest{}, opts...)
	if err != nil {
		klog.Errorf("ImageFsInfo from image service failed: %v", err)
		return nil, err
	}
	return resp.GetImageFilesystems(), nil
}
