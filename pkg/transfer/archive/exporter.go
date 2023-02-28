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

type ExportOptions struct {
	Images               []string
	Platforms            []v1.Platform
	AllPlatforms         bool
	SkipDockerManifest   bool
	SkipNonDistributable bool
}

// NewImageExportStream returns an image exporter via tar stream
func NewImageExportStream(stream io.WriteCloser, mediaType string, opts ExportOptions) *ImageExportStream {
	return &ImageExportStream{
		stream:    stream,
		mediaType: mediaType,

		images:               opts.Images,
		platforms:            opts.Platforms,
		allPlatforms:         opts.AllPlatforms,
		skipDockerManifest:   opts.SkipDockerManifest,
		skipNonDistributable: opts.SkipNonDistributable,
	}
}

type ImageExportStream struct {
	stream    io.WriteCloser
	mediaType string

	images               []string
	platforms            []v1.Platform
	allPlatforms         bool
	skipDockerManifest   bool
	skipNonDistributable bool
}

func (iis *ImageExportStream) ExportStream(context.Context) (io.WriteCloser, string, error) {
	return iis.stream, iis.mediaType, nil
}

func (iis *ImageExportStream) Export(ctx context.Context, is images.Store, cs content.Store) error {
	var opts []archive.ExportOpt
	for _, img := range iis.images {
		opts = append(opts, archive.WithImage(is, img))
	}
	if len(iis.platforms) > 0 {
		opts = append(opts, archive.WithPlatform(platforms.Ordered(iis.platforms...)))
	} else {
		opts = append(opts, archive.WithPlatform(platforms.DefaultStrict()))
	}
	if iis.allPlatforms {
		opts = append(opts, archive.WithAllPlatforms())
	}
	if iis.skipDockerManifest {
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
		Stream:               sid,
		MediaType:            iis.mediaType,
		Images:               iis.images,
		Platforms:            specified,
		AllPlatforms:         iis.allPlatforms,
		SkipDockerManifest:   iis.skipDockerManifest,
		SkipNonDistributable: iis.skipNonDistributable,
	}

	return typeurl.MarshalAny(s)
}

func (iis *ImageExportStream) UnmarshalAny(ctx context.Context, sm streaming.StreamGetter, any typeurl.Any) error {
	var s transfertypes.ImageExportStream
	if err := typeurl.UnmarshalTo(any, &s); err != nil {
		return err
	}

	stream, err := sm.Get(ctx, s.Stream)
	if err != nil {
		log.G(ctx).WithError(err).WithField("stream", s.Stream).Debug("failed to get export stream")
		return err
	}

	var specified []v1.Platform
	for _, p := range s.Platforms {
		specified = append(specified, v1.Platform{
			OS:           p.OS,
			Architecture: p.Architecture,
			Variant:      p.Variant,
		})
	}

	iis.stream = tstreaming.WriteByteStream(ctx, stream)
	iis.mediaType = s.MediaType
	iis.images = s.Images
	iis.platforms = specified
	iis.allPlatforms = s.AllPlatforms
	iis.skipDockerManifest = s.SkipDockerManifest
	iis.skipNonDistributable = s.SkipNonDistributable

	return nil
}
