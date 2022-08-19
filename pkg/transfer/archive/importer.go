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

	transferapi "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/streaming"
	tstreaming "github.com/containerd/containerd/pkg/transfer/streaming"
	"github.com/containerd/typeurl"
)

// NewImageImportStream returns a image importer via tar stream
// TODO: Add import options
func NewImageImportStream(stream io.Reader) *ImageImportStream {
	return &ImageImportStream{
		stream: stream,
	}
}

type ImageImportStream struct {
	stream io.Reader
}

func (iis *ImageImportStream) ImportStream(context.Context) (io.Reader, error) {
	return iis.stream, nil
}

func (iis *ImageImportStream) MarshalAny(ctx context.Context, sm streaming.StreamCreator) (typeurl.Any, error) {
	sid := tstreaming.GenerateID("import")
	stream, err := sm.Create(ctx, sid)
	if err != nil {
		return nil, err
	}
	tstreaming.SendStream(ctx, iis.stream, stream)

	s := &transferapi.ImageImportStream{
		Stream: sid,
	}

	return typeurl.MarshalAny(s)
}

func (iis *ImageImportStream) UnmarshalAny(ctx context.Context, sm streaming.StreamGetter, any typeurl.Any) error {
	var s transferapi.ImageImportStream
	if err := typeurl.UnmarshalTo(any, &s); err != nil {
		return err
	}

	stream, err := sm.Get(ctx, s.Stream)
	if err != nil {
		log.G(ctx).WithError(err).WithField("stream", s.Stream).Debug("failed to get import stream")
		return err
	}

	iis.stream = tstreaming.ReceiveStream(ctx, stream)

	return nil
}
