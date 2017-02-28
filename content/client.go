package content

import (
	"context"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	contentapi "github.com/docker/containerd/api/services/content"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

func NewProviderFromClient(client contentapi.ContentClient) Provider {
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

func (rr *remoteReader) Close() error {
	return rr.client.CloseSend()
}

func NewIngesterFromClient(client contentapi.ContentClient) Ingester {
	return &remoteIngester{
		client: client,
	}
}

type remoteIngester struct {
	client contentapi.ContentClient
}

func (ri *remoteIngester) Writer(ctx context.Context, ref string, size int64, expected digest.Digest) (Writer, error) {
	wrclient, offset, err := ri.negotiate(ctx, ref, size, expected)
	if err != nil {
		return nil, rewriteGRPCError(err)
	}

	return &remoteWriter{
		client: wrclient,
		offset: offset,
	}, nil
}

func (ri *remoteIngester) negotiate(ctx context.Context, ref string, size int64, expected digest.Digest) (contentapi.Content_WriteClient, int64, error) {
	wrclient, err := ri.client.Write(ctx)
	if err != nil {
		return nil, 0, err
	}

	if err := wrclient.Send(&contentapi.WriteRequest{
		Action:   contentapi.WriteActionStat,
		Ref:      ref,
		Total:    size,
		Expected: expected,
	}); err != nil {
		return nil, 0, err
	}

	resp, err := wrclient.Recv()
	if err != nil {
		return nil, 0, err
	}

	return wrclient, resp.Offset, nil
}

type remoteWriter struct {
	ref    string
	client contentapi.Content_WriteClient
	offset int64
	digest digest.Digest
}

func newRemoteWriter(client contentapi.Content_WriteClient, ref string, offset int64) (*remoteWriter, error) {
	return &remoteWriter{
		ref:    ref,
		client: client,
		offset: offset,
	}, nil
}

// send performs a synchronous req-resp cycle on the client.
func (rw *remoteWriter) send(req *contentapi.WriteRequest) (*contentapi.WriteResponse, error) {
	if err := rw.client.Send(req); err != nil {
		return nil, err
	}

	resp, err := rw.client.Recv()

	if err == nil {
		// try to keep these in sync
		if resp.Digest != "" {
			rw.digest = resp.Digest
		}
	}

	return resp, err
}

func (rw *remoteWriter) Status() (Status, error) {
	resp, err := rw.send(&contentapi.WriteRequest{
		Action: contentapi.WriteActionStat,
	})
	if err != nil {
		return Status{}, err
	}

	return Status{
		Ref:       rw.ref,
		Offset:    resp.Offset,
		StartedAt: resp.StartedAt,
		UpdatedAt: resp.UpdatedAt,
	}, nil
}

func (rw *remoteWriter) Digest() digest.Digest {
	return rw.digest
}

func (rw *remoteWriter) Write(p []byte) (n int, err error) {
	offset := rw.offset

	resp, err := rw.send(&contentapi.WriteRequest{
		Action: contentapi.WriteActionWrite,
		Offset: offset,
		Data:   p,
	})
	if err != nil {
		return 0, err
	}

	n = int(resp.Offset - offset)
	if n < len(p) {
		err = io.ErrShortWrite
	}

	rw.offset += int64(n)
	return
}

func (rw *remoteWriter) Commit(size int64, expected digest.Digest) error {
	resp, err := rw.send(&contentapi.WriteRequest{
		Action:   contentapi.WriteActionCommit,
		Total:    size,
		Offset:   rw.offset,
		Expected: expected,
	})
	if err != nil {
		return rewriteGRPCError(err)
	}

	if size != 0 && resp.Offset != size {
		return errors.Errorf("unexpected size: %v != %v", resp.Offset, size)
	}

	if expected != "" && resp.Digest != expected {
		return errors.Errorf("unexpected digest: %v != %v", resp.Digest, expected)
	}

	return nil
}

func (rw *remoteWriter) Truncate(size int64) error {
	// This truncation won't actually be validated until a write is issued.
	rw.offset = size
	return nil
}

func (rw *remoteWriter) Close() error {
	return rw.client.CloseSend()
}

func rewriteGRPCError(err error) error {
	switch grpc.Code(errors.Cause(err)) {
	case codes.AlreadyExists:
		return errExists
	case codes.NotFound:
		return errNotFound
	}

	return err
}
