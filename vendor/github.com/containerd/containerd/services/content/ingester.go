package content

import (
	"context"
	"io"

	contentapi "github.com/containerd/containerd/api/services/content"
	"github.com/containerd/containerd/content"

	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

func NewIngesterFromClient(client contentapi.ContentClient) content.Ingester {
	return &remoteIngester{
		client: client,
	}
}

type remoteIngester struct {
	client contentapi.ContentClient
}

func (ri *remoteIngester) Writer(ctx context.Context, ref string, size int64, expected digest.Digest) (content.Writer, error) {
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

func (rw *remoteWriter) Status() (content.Status, error) {
	resp, err := rw.send(&contentapi.WriteRequest{
		Action: contentapi.WriteActionStat,
	})
	if err != nil {
		return content.Status{}, err
	}

	return content.Status{
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
