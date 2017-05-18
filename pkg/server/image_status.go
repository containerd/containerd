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

// ImageStatus returns the status of the image, returns nil if the image isn't present.
// TODO(random-liu): We should change CRI to distinguish image id and image spec. (See
// kubernetes/kubernetes#46255)
func (c *criContainerdService) ImageStatus(ctx context.Context, r *runtime.ImageStatusRequest) (retRes *runtime.ImageStatusResponse, retErr error) {
	glog.V(4).Infof("ImageStatus for image %q", r.GetImage().GetImage())
	defer func() {
		if retErr == nil {
			glog.V(4).Infof("ImageStatus for %q returns image status %+v",
				r.GetImage().GetImage(), retRes.GetImage())
		}
	}()
	imageID, err := c.localResolve(ctx, r.GetImage().GetImage())
	if err != nil {
		return nil, fmt.Errorf("can not resolve %q locally: %v", r.GetImage().GetImage(), err)
	}
	if imageID == "" {
		// return empty without error when image not found.
		return &runtime.ImageStatusResponse{}, nil
	}

	meta, err := c.imageMetadataStore.Get(imageID)
	if err != nil {
		if metadata.IsNotExistError(err) {
			return &runtime.ImageStatusResponse{}, nil
		}
		return nil, fmt.Errorf("an error occurred during get image %q metadata: %v",
			imageID, err)
	}
	// TODO(random-liu): [P0] Make sure corresponding snapshot exists. What if snapshot
	// doesn't exist?
	runtimeImage := &runtime.Image{
		Id:          meta.ID,
		RepoTags:    meta.RepoTags,
		RepoDigests: meta.RepoDigests,
		Size_:       uint64(meta.Size),
	}
	uid, username := getUserFromImage(meta.Config.User)
	if uid != nil {
		runtimeImage.Uid = &runtime.Int64Value{Value: *uid}
	}
	runtimeImage.Username = username

	// TODO(mikebrow): write a ImageMetadata to runtim.Image converter
	return &runtime.ImageStatusResponse{Image: runtimeImage}, nil
}
