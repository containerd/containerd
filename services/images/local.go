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

package images

import (
	"context"

	"github.com/containerd/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	eventstypes "github.com/containerd/containerd/api/events"
	imagesapi "github.com/containerd/containerd/api/services/images/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/gc"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/pkg/deprecation"
	"github.com/containerd/containerd/pkg/epoch"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/plugin/registry"
	"github.com/containerd/containerd/plugins"
	ptypes "github.com/containerd/containerd/protobuf/types"
	"github.com/containerd/containerd/services"
	"github.com/containerd/containerd/services/warning"
)

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.ServicePlugin,
		ID:   services.ImagesService,
		Requires: []plugin.Type{
			plugins.EventPlugin,
			plugins.MetadataPlugin,
			plugins.GCPlugin,
			plugins.WarningPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			m, err := ic.Get(plugins.MetadataPlugin)
			if err != nil {
				return nil, err
			}
			g, err := ic.Get(plugins.GCPlugin)
			if err != nil {
				return nil, err
			}
			ep, err := ic.Get(plugins.EventPlugin)
			if err != nil {
				return nil, err
			}
			w, err := ic.Get(plugins.WarningPlugin)
			if err != nil {
				return nil, err
			}

			return &local{
				store:     metadata.NewImageStore(m.(*metadata.DB)),
				publisher: ep.(events.Publisher),
				gc:        g.(gcScheduler),
				warnings:  w.(warning.Service),
			}, nil
		},
	})
}

type gcScheduler interface {
	ScheduleAndWait(context.Context) (gc.Stats, error)
}

type local struct {
	store     images.Store
	gc        gcScheduler
	publisher events.Publisher
	warnings  warning.Service
}

var _ imagesapi.ImagesClient = &local{}

func (l *local) Get(ctx context.Context, req *imagesapi.GetImageRequest, _ ...grpc.CallOption) (*imagesapi.GetImageResponse, error) {
	image, err := l.store.Get(ctx, req.Name)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	imagepb := imageToProto(&image)
	return &imagesapi.GetImageResponse{
		Image: imagepb,
	}, nil
}

func (l *local) List(ctx context.Context, req *imagesapi.ListImagesRequest, _ ...grpc.CallOption) (*imagesapi.ListImagesResponse, error) {
	images, err := l.store.List(ctx, req.Filters...)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &imagesapi.ListImagesResponse{
		Images: imagesToProto(images),
	}, nil
}

func (l *local) Create(ctx context.Context, req *imagesapi.CreateImageRequest, _ ...grpc.CallOption) (*imagesapi.CreateImageResponse, error) {
	log.G(ctx).WithField("name", req.Image.Name).WithField("target", req.Image.Target.Digest).Debugf("create image")
	if req.Image.Name == "" {
		return nil, status.Errorf(codes.InvalidArgument, "Image.Name required")
	}

	var (
		image = imageFromProto(req.Image)
		resp  imagesapi.CreateImageResponse
	)
	if req.SourceDateEpoch != nil {
		tm := req.SourceDateEpoch.AsTime()
		ctx = epoch.WithSourceDateEpoch(ctx, &tm)
	}
	created, err := l.store.Create(ctx, image)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	resp.Image = imageToProto(&created)

	if err := l.publisher.Publish(ctx, "/images/create", &eventstypes.ImageCreate{
		Name:   resp.Image.Name,
		Labels: resp.Image.Labels,
	}); err != nil {
		return nil, err
	}

	l.emitSchema1DeprecationWarning(ctx, &image)
	return &resp, nil

}

func (l *local) Update(ctx context.Context, req *imagesapi.UpdateImageRequest, _ ...grpc.CallOption) (*imagesapi.UpdateImageResponse, error) {
	if req.Image.Name == "" {
		return nil, status.Errorf(codes.InvalidArgument, "Image.Name required")
	}

	var (
		image      = imageFromProto(req.Image)
		resp       imagesapi.UpdateImageResponse
		fieldpaths []string
	)

	if req.UpdateMask != nil && len(req.UpdateMask.Paths) > 0 {
		fieldpaths = append(fieldpaths, req.UpdateMask.Paths...)
	}

	if req.SourceDateEpoch != nil {
		tm := req.SourceDateEpoch.AsTime()
		ctx = epoch.WithSourceDateEpoch(ctx, &tm)
	}

	updated, err := l.store.Update(ctx, image, fieldpaths...)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	resp.Image = imageToProto(&updated)

	if err := l.publisher.Publish(ctx, "/images/update", &eventstypes.ImageUpdate{
		Name:   resp.Image.Name,
		Labels: resp.Image.Labels,
	}); err != nil {
		return nil, err
	}

	l.emitSchema1DeprecationWarning(ctx, &image)
	return &resp, nil
}

func (l *local) Delete(ctx context.Context, req *imagesapi.DeleteImageRequest, _ ...grpc.CallOption) (*ptypes.Empty, error) {
	log.G(ctx).WithField("name", req.Name).Debugf("delete image")

	var opts []images.DeleteOpt
	if req.Target != nil {
		desc := descFromProto(req.Target)
		opts = append(opts, images.DeleteTarget(&desc))
	}

	// Sync option handled here after event is published
	if err := l.store.Delete(ctx, req.Name, opts...); err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	if err := l.publisher.Publish(ctx, "/images/delete", &eventstypes.ImageDelete{
		Name: req.Name,
	}); err != nil {
		return nil, err
	}

	if req.Sync {
		if _, err := l.gc.ScheduleAndWait(ctx); err != nil {
			return nil, err
		}
	}

	return &ptypes.Empty{}, nil
}

func (l *local) emitSchema1DeprecationWarning(ctx context.Context, image *images.Image) {
	if image == nil {
		return
	}
	dgst, ok := image.Labels[images.ConvertedDockerSchema1LabelKey]
	if !ok {
		return
	}
	log.G(ctx).WithField("name", image.Name).WithField("schema1digest", dgst).Warn("conversion from schema 1 images is deprecated")
	l.warnings.Emit(ctx, deprecation.PullSchema1Image)
}
