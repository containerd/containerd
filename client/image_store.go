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

package client

import (
	"context"
	"strings"

	imagesapi "github.com/containerd/containerd/v2/api/services/images/v1"
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/images"
	ctrdlabels "github.com/containerd/containerd/v2/labels"
	"github.com/containerd/containerd/v2/oci"
	"github.com/containerd/containerd/v2/pkg/cri/util"
	"github.com/containerd/containerd/v2/pkg/epoch"
	"github.com/containerd/containerd/v2/protobuf"
	ptypes "github.com/containerd/containerd/v2/protobuf/types"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type remoteImages struct {
	client imagesapi.ImagesClient
}

// NewImageStoreFromClient returns a new image store client
func NewImageStoreFromClient(client imagesapi.ImagesClient) images.Store {
	return &remoteImages{
		client: client,
	}
}

func (s *remoteImages) Get(ctx context.Context, name string) (images.Image, error) {
	resp, err := s.client.Get(ctx, &imagesapi.GetImageRequest{
		Name: name,
	})
	if err != nil {
		return images.Image{}, errdefs.FromGRPC(err)
	}

	return imageFromProto(resp.Image), nil
}

func (s *remoteImages) List(ctx context.Context, filters ...string) ([]images.Image, error) {
	resp, err := s.client.List(ctx, &imagesapi.ListImagesRequest{
		Filters: filters,
	})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}

	return imagesFromProto(resp.Images), nil
}

func (s *remoteImages) Create(ctx context.Context, image images.Image) (images.Image, error) {
	// get runtimehandler image label from the image
	runtimeHandler := ""
	for labelKey, _ := range image.Labels {
		if strings.HasPrefix(labelKey, ctrdlabels.RuntimeHandlerLabelPrefix) {
			runtimeHandler = strings.TrimPrefix(labelKey, ctrdlabels.RuntimeHandlerLabelPrefix+".")
			break
		}
	}

	req := &imagesapi.CreateImageRequest{
		Image:          imageToProto(&image),
		RuntimeHandler: runtimeHandler,
	}
	if tm := epoch.FromContext(ctx); tm != nil {
		req.SourceDateEpoch = timestamppb.New(*tm)
	}
	created, err := s.client.Create(ctx, req)
	if err != nil {
		return images.Image{}, errdefs.FromGRPC(err)
	}

	return imageFromProto(created.Image), nil
}

func getNewRuntimeHandlerLabel(image images.Image, fieldpaths *[]string) string {
	for _, value := range *fieldpaths {
		if strings.Contains(value, "newRuntimeHandler.") {

			runtimeHandler := strings.TrimPrefix(value, "newRuntimeHandler.")
			*fieldpaths = util.SubtractStringSlice(*fieldpaths, value)
			return runtimeHandler
		}
	}
	return ""
}

func (s *remoteImages) Update(ctx context.Context, image images.Image, fieldpaths ...string) (images.Image, error) {

	// Check if the image exists and if so find out the newest
	// runtimeHandler label to add to this image from fieldpaths
	runtimeHandler := getNewRuntimeHandlerLabel(image, &fieldpaths)

	var updateMask *ptypes.FieldMask
	if len(fieldpaths) > 0 {
		updateMask = &ptypes.FieldMask{
			Paths: fieldpaths,
		}
	}
	req := &imagesapi.UpdateImageRequest{
		Image:          imageToProto(&image),
		UpdateMask:     updateMask,
		RuntimeHandler: runtimeHandler,
	}
	if tm := epoch.FromContext(ctx); tm != nil {
		req.SourceDateEpoch = timestamppb.New(*tm)
	}
	updated, err := s.client.Update(ctx, req)
	if err != nil {
		return images.Image{}, errdefs.FromGRPC(err)
	}

	return imageFromProto(updated.Image), nil
}

func (s *remoteImages) Delete(ctx context.Context, name string, opts ...images.DeleteOpt) error {
	var do images.DeleteOptions
	for _, opt := range opts {
		if err := opt(ctx, &do); err != nil {
			return err
		}
	}
	req := &imagesapi.DeleteImageRequest{
		Name:           name,
		Sync:           do.Synchronous,
		RuntimeHandler: do.RuntimeHandler,
	}
	if do.Target != nil {
		req.Target = oci.DescriptorToProto(*do.Target)
	}
	_, err := s.client.Delete(ctx, req)
	return errdefs.FromGRPC(err)
}

func imageToProto(image *images.Image) *imagesapi.Image {
	return &imagesapi.Image{
		Name:      image.Name,
		Labels:    image.Labels,
		Target:    oci.DescriptorToProto(image.Target),
		CreatedAt: protobuf.ToTimestamp(image.CreatedAt),
		UpdatedAt: protobuf.ToTimestamp(image.UpdatedAt),
	}
}

func imageFromProto(imagepb *imagesapi.Image) images.Image {
	return images.Image{
		Name:      imagepb.Name,
		Labels:    imagepb.Labels,
		Target:    oci.DescriptorFromProto(imagepb.Target),
		CreatedAt: protobuf.FromTimestamp(imagepb.CreatedAt),
		UpdatedAt: protobuf.FromTimestamp(imagepb.UpdatedAt),
	}
}

func imagesFromProto(imagespb []*imagesapi.Image) []images.Image {
	var images []images.Image

	for _, image := range imagespb {
		image := image
		images = append(images, imageFromProto(image))
	}

	return images
}
