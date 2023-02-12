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

	transfertypes "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/streaming"
	"github.com/containerd/containerd/pkg/transfer/plugins"
	tstreaming "github.com/containerd/containerd/pkg/transfer/streaming"
	"github.com/containerd/typeurl/v2"
)

func init() {
	// TODO: Move this to separate package?
	plugins.Register(&transfertypes.ImageExportStream{}, &ImageExportStream{})
	plugins.Register(&transfertypes.ImageImportStream{}, &ImageImportStream{})
}

// NewImageExportStream returns a image importer via tar stream
// TODO: Add export options
func NewImageExportStream(stream io.WriteCloser, mediaType string) *ImageExportStream {
	return &ImageExportStream{
		stream:    stream,
		mediaType: mediaType,
	}
}

type ImageExportStream struct {
	stream    io.WriteCloser
	mediaType string
}

func (iis *ImageExportStream) ExportStream(context.Context) (io.WriteCloser, string, error) {
	return iis.stream, iis.mediaType, nil
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

	s := &transfertypes.ImageExportStream{
		Stream:    sid,
		MediaType: iis.mediaType,
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

	iis.stream = tstreaming.WriteByteStream(ctx, stream)
	iis.mediaType = s.MediaType

	return nil
}
