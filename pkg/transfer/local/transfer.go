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

package local

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/pkg/transfer"
	"github.com/containerd/typeurl/v2"
	"golang.org/x/sync/semaphore"
)

type localTransferService struct {
	leases  leases.Manager
	content content.Store
	images  images.Store

	// semaphore.NewWeighted(int64(rCtx.MaxConcurrentDownloads))
	limiter *semaphore.Weighted

	// TODO: Duplication suppressor

	// Configuration
	//  - Max downloads
	//  - Max uploads

	// Supported platforms
	//  - Platform -> snapshotter defaults?
}

func NewTransferService(lm leases.Manager, cs content.Store, is images.Store) transfer.Transferrer {
	return &localTransferService{
		leases:  lm,
		content: cs,
		images:  is,
	}
}

func (ts *localTransferService) Transfer(ctx context.Context, src interface{}, dest interface{}, opts ...transfer.Opt) error {
	topts := &transfer.Config{}
	for _, opt := range opts {
		opt(topts)
	}

	// Figure out matrix of whether source destination combination is supported
	switch s := src.(type) {
	case transfer.ImageFetcher:
		switch d := dest.(type) {
		case transfer.ImageStorer:
			return ts.pull(ctx, s, d, topts)
		}
	case transfer.ImageGetter:
		switch d := dest.(type) {
		case transfer.ImagePusher:
			return ts.push(ctx, s, d, topts)
		}
	case transfer.ImageImporter:
		switch d := dest.(type) {
		case transfer.ImageExportStreamer:
			return ts.echo(ctx, s, d, topts)
		case transfer.ImageStorer:
			return ts.importStream(ctx, s, d, topts)
		}
	}
	return fmt.Errorf("unable to transfer from %s to %s: %w", name(src), name(dest), errdefs.ErrNotImplemented)
}

func name(t interface{}) string {
	switch s := t.(type) {
	case fmt.Stringer:
		return s.String()
	case typeurl.Any:
		return s.GetTypeUrl()
	default:
		return fmt.Sprintf("%T", t)
	}
}

// echo is mostly used for testing, it implements an import->export which is
// a no-op which only roundtrips the bytes.
func (ts *localTransferService) echo(ctx context.Context, i transfer.ImageImporter, e transfer.ImageExportStreamer, tops *transfer.Config) error {
	iis, ok := i.(transfer.ImageImportStreamer)
	if !ok {
		return fmt.Errorf("echo requires access to raw stream: %w", errdefs.ErrNotImplemented)
	}
	r, _, err := iis.ImportStream(ctx)
	if err != nil {
		return err
	}
	wc, _, err := e.ExportStream(ctx)
	if err != nil {
		return err
	}

	// TODO: Use fixed buffer? Send write progress?
	_, err = io.Copy(wc, r)
	if werr := wc.Close(); werr != nil && err == nil {
		err = werr
	}
	return err
}

// WithLease attaches a lease on the context
func (ts *localTransferService) withLease(ctx context.Context, opts ...leases.Opt) (context.Context, func(context.Context) error, error) {
	nop := func(context.Context) error { return nil }

	_, ok := leases.FromContext(ctx)
	if ok {
		return ctx, nop, nil
	}

	ls := ts.leases

	if len(opts) == 0 {
		// Use default lease configuration if no options provided
		opts = []leases.Opt{
			leases.WithRandomID(),
			leases.WithExpiration(24 * time.Hour),
		}
	}

	l, err := ls.Create(ctx, opts...)
	if err != nil {
		return ctx, nop, err
	}

	ctx = leases.WithLease(ctx, l.ID)
	return ctx, func(ctx context.Context) error {
		return ls.Delete(ctx, l)
	}, nil
}
