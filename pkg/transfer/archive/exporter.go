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

package archive

import (
	"context"
	"io"

	"github.com/containerd/typeurl/v2"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/api/types"
	transfertypes "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/images/archive"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/streaming"
	"github.com/containerd/containerd/pkg/transfer/plugins"
	tstreaming "github.com/containerd/containerd/pkg/transfer/streaming"
	"github.com/containerd/containerd/platforms"
)

func init() {
	// TODO: Move this to separate package?
	plugins.Register(&transfertypes.ImageExportStream{}, &ImageExportStream{})
	plugins.Register(&transfertypes.ImageImportStream{}, &ImageImportStream{})
}

type ExportOpt func(*ImageExportStream)

func WithPlatform(p v1.Platform) ExportOpt {
	return func(s *ImageExportStream) {
		s.platforms = append(s.platforms, p)
	}
}

func WithAllPlatforms(s *ImageExportStream) {
	s.allPlatforms = true
}

func WithSkipCompatibilityManifest(s *ImageExportStream) {
	s.skipCompatibilityManifest = true
}

func WithSkipNonDistributableBlobs(s *ImageExportStream) {
	s.skipNonDistributable = true
}

// NewImageExportStream returns an image exporter via tar stream
func NewImageExportStream(stream io.WriteCloser, mediaType string, opts ...ExportOpt) *ImageExportStream {
	s := &ImageExportStream{
		stream:    stream,
		mediaType: mediaType,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type ImageExportStream struct {
	stream    io.WriteCloser
	mediaType string

	platforms                 []v1.Platform
	allPlatforms              bool
	skipCompatibilityManifest bool
	skipNonDistributable      bool
}

func (iis *ImageExportStream) ExportStream(context.Context) (io.WriteCloser, string, error) {
	return iis.stream, iis.mediaType, nil
}

func (iis *ImageExportStream) Export(ctx context.Context, cs content.Store, imgs []images.Image) error {
	opts := []archive.ExportOpt{
		archive.WithImages(imgs),
	}

	if len(iis.platforms) > 0 {
		opts = append(opts, archive.WithPlatform(platforms.Ordered(iis.platforms...)))
	} else {
		opts = append(opts, archive.WithPlatform(platforms.DefaultStrict()))
	}
	if iis.allPlatforms {
		opts = append(opts, archive.WithAllPlatforms())
	}
	if iis.skipCompatibilityManifest {
		opts = append(opts, archive.WithSkipDockerManifest())
	}
	if iis.skipNonDistributable {
		opts = append(opts, archive.WithSkipNonDistributableBlobs())
	}
	return archive.Export(ctx, cs, iis.stream, opts...)
}

func (iis *ImageExportStream) MarshalAny(ctx context.Context, sm streaming.StreamCreator) (typeurl.Any, error) {
	sid := tstreaming.GenerateID("export")
	stream, err := sm.Create(ctx, sid)
	if err != nil {
		return nil, err
	}

	// Receive stream and copy to writer
	go func() {
		if _, err := io.Copy(iis.stream, tstreaming.ReceiveStream(ctx, stream)); err != nil {
			log.G(ctx).WithError(err).WithField("streamid", sid).Errorf("error copying stream")
		}
		iis.stream.Close()
	}()

	var specified []*types.Platform
	for _, p := range iis.platforms {
		specified = append(specified, &types.Platform{
			OS:           p.OS,
			Architecture: p.Architecture,
			Variant:      p.Variant,
		})
	}
	s := &transfertypes.ImageExportStream{
		Stream:                    sid,
		MediaType:                 iis.mediaType,
		Platforms:                 specified,
		AllPlatforms:              iis.allPlatforms,
		SkipCompatibilityManifest: iis.skipCompatibilityManifest,
		SkipNonDistributable:      iis.skipNonDistributable,
	}

	return typeurl.MarshalAny(s)
}

func (iis *ImageExportStream) UnmarshalAny(ctx context.Context, sm streaming.StreamGetter, anyType typeurl.Any) error {
	var s transfertypes.ImageExportStream
	if err := typeurl.UnmarshalTo(anyType, &s); err != nil {
		return err
	}

	stream, err := sm.Get(ctx, s.Stream)
	if err != nil {
		log.G(ctx).WithError(err).WithField("stream", s.Stream).Debug("failed to get export stream")
		return err
	}

	specified := types.OCIPlatformFromProto(s.Platforms)
	iis.stream = tstreaming.WriteByteStream(ctx, stream)
	iis.mediaType = s.MediaType
	iis.platforms = specified
	iis.allPlatforms = s.AllPlatforms
	iis.skipCompatibilityManifest = s.SkipCompatibilityManifest
	iis.skipNonDistributable = s.SkipNonDistributable

	return nil
}
