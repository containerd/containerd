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
	"fmt"

	"github.com/golang/glog"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
)

// ListImages lists existing images.
// TODO(random-liu): Add image list filters after CRI defines this more clear, and kubelet
// actually needs it.
func (c *criContainerdService) ListImages(ctx context.Context, r *runtime.ListImagesRequest) (retRes *runtime.ListImagesResponse, retErr error) {
	glog.V(4).Infof("ListImages with filter %+v", r.GetFilter())
	defer func() {
		if retErr == nil {
			glog.V(4).Infof("ListImages returns image list %+v", retRes.GetImages())
		}
	}()

	imageMetadataA, err := c.imageMetadataStore.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list image metadata from store: %v", err)
	}
	var images []*runtime.Image
	for _, image := range imageMetadataA {
		// TODO(random-liu): [P0] Make sure corresponding snapshot exists. What if snapshot
		// doesn't exist?
		images = append(images, toCRIImage(image))
	}

	return &runtime.ListImagesResponse{Images: images}, nil
}

// toCRIImage converts image metadata to CRI image type.
func toCRIImage(image *metadata.ImageMetadata) *runtime.Image {
	runtimeImage := &runtime.Image{
		Id:          image.ID,
		RepoTags:    image.RepoTags,
		RepoDigests: image.RepoDigests,
		Size_:       uint64(image.Size),
	}
	uid, username := getUserFromImage(image.Config.User)
	if uid != nil {
		runtimeImage.Uid = &runtime.Int64Value{Value: *uid}
	}
	runtimeImage.Username = username
	return runtimeImage
}
