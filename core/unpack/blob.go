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

package unpack

import (
	"context"
	"errors"
	"sync"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/pkg/tracing"
)

// blobSet tracks downloads by layer position. Layers with the same digest still
// invoke the handler separately. With a limiter, downloads acquire slots in
// request order. The caller controls group cancellation and waits for completion.
type blobSet struct {
	u     *Unpacker
	h     images.Handler
	group *errgroup.Group

	mu    sync.Mutex
	blobs map[int]*blob
	// admitted is closed after the most recently requested blob acquires a
	// download limiter slot or stops waiting for one.
	admitted chan struct{}
}

type blob struct {
	// err is ready to read after done closes.
	done chan struct{}
	err  error
}

func newBlobSet(u *Unpacker, h images.Handler, group *errgroup.Group) *blobSet {
	admitted := make(chan struct{})
	close(admitted)
	return &blobSet{
		u:        u,
		h:        h,
		group:    group,
		blobs:    make(map[int]*blob),
		admitted: admitted,
	}
}

// fetch starts a download for layer i or returns its existing result.
// The first request supplies the download's descriptor and context.
func (s *blobSet) fetch(ctx context.Context, i int, desc ocispec.Descriptor) *blob {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.blobs[i]; ok {
		return b
	}
	b := &blob{done: make(chan struct{})}
	s.blobs[i] = b
	prev, admitted := s.admitted, make(chan struct{})
	s.admitted = admitted
	s.group.Go(func() error {
		defer close(b.done)
		b.err = s.download(ctx, desc, prev, admitted)
		return b.err
	})
	return b
}

// download closes admitted once it acquires a limiter slot or stops waiting
// for one. The download itself may still be running.
func (s *blobSet) download(ctx context.Context, desc ocispec.Descriptor, prev <-chan struct{}, admitted chan<- struct{}) error {
	ctx, span := tracing.StartSpan(ctx, tracing.Name(unpackSpanPrefix, "fetchLayer"))
	defer span.End()
	setLayerAttributes(span, desc)

	err := waitClosed(ctx, prev)
	if err == nil {
		err = s.u.acquire(ctx, s.u.limiter)
	}
	close(admitted)
	if err != nil {
		return err
	}
	defer s.u.release(s.u.limiter)

	unlock, err := s.u.lockBlobDescriptor(ctx, desc)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := s.h.Handle(ctx, desc); err != nil && !errors.Is(err, images.ErrSkipDesc) {
		return err
	}
	return nil
}

func setLayerAttributes(span *tracing.Span, desc ocispec.Descriptor) {
	span.SetAttributes(
		tracing.Attribute("layer.media.type", desc.MediaType),
		tracing.Attribute("layer.media.size", desc.Size),
		tracing.Attribute("layer.media.digest", desc.Digest.String()),
	)
}
