package content

import (
	"context"
	"io"

	contentapi "github.com/docker/containerd/api/services/content"
	"github.com/docker/containerd/content"
	digest "github.com/opencontainers/go-digest"
)

func NewProviderFromClient(client contentapi.ContentClient) content.Provider {
	return &remoteProvider{
		client: client,
	}
}

type remoteProvider struct {
	client contentapi.ContentClient
}

func (rp *remoteProvider) Reader(ctx context.Context, dgst digest.Digest) (io.ReadCloser, error) {
	client, err := rp.client.Read(ctx, &contentapi.ReadRequest{Digest: dgst})
	if err != nil {
		return nil, err
	}

	return &remoteReader{
		client: client,
	}, nil
}

type remoteReader struct {
	client contentapi.Content_ReadClient
	extra  []byte
}

func (rr *remoteReader) Read(p []byte) (n int, err error) {
	n += copy(p, rr.extra)
	if n >= len(p) {
		if n <= len(rr.extra) {
			rr.extra = rr.extra[n:]
		} else {
			rr.extra = rr.extra[:0]
		}
		return
	}

	p = p[n:]
	for len(p) > 0 {
		var resp *contentapi.ReadResponse
		// fill our buffer up until we can fill p.
		resp, err = rr.client.Recv()
		if err != nil {
			return
		}

		copied := copy(p, resp.Data)
		n += copied
		p = p[copied:]

		if copied < len(p) {
			continue
		}

		rr.extra = append(rr.extra, resp.Data[copied:]...)
	}

	return
}

// TODO(stevvooe): Implemente io.ReaderAt.

func (rr *remoteReader) Close() error {
	return rr.client.CloseSend()
}
