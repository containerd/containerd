package content

import (
	"context"

	contentapi "github.com/containerd/containerd/api/services/content"
	digest "github.com/opencontainers/go-digest"
)

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
	rr.extra = rr.extra[:0]

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

		if len(p) == 0 {
			rr.extra = append(rr.extra, resp.Data[copied:]...)
		}
	}

	return
}

func (rr *remoteReader) Close() error {
	return rr.client.CloseSend()
}

type remoteReaderAt struct {
	ctx    context.Context
	digest digest.Digest
	client contentapi.ContentClient
}

func (ra *remoteReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	rr := &contentapi.ReadRequest{
		Digest: ra.digest,
		Offset: off,
		Size_:  int64(len(p)),
	}
	rc, err := ra.client.Read(ra.ctx, rr)
	if err != nil {
		return 0, err
	}

	for len(p) > 0 {
		var resp *contentapi.ReadResponse
		// fill our buffer up until we can fill p.
		resp, err = rc.Recv()
		if err != nil {
			return n, err
		}

		copied := copy(p, resp.Data)
		n += copied
		p = p[copied:]
	}
	return n, nil
}
