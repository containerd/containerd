package content

import (
	"context"
	"io"

	contentapi "github.com/containerd/containerd/api/services/content"
	"github.com/containerd/containerd/content"
	digest "github.com/opencontainers/go-digest"
)

type remoteStore struct {
	client contentapi.ContentClient
}

func NewStoreFromClient(client contentapi.ContentClient) content.Store {
	return &remoteStore{
		client: client,
	}
}

func (rs *remoteStore) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	resp, err := rs.client.Info(ctx, &contentapi.InfoRequest{
		Digest: dgst,
	})
	if err != nil {
		return content.Info{}, rewriteGRPCError(err)
	}

	return content.Info{
		Digest:      resp.Info.Digest,
		Size:        resp.Info.Size_,
		CommittedAt: resp.Info.CommittedAt,
	}, nil
}

func (rs *remoteStore) Walk(ctx context.Context, fn content.WalkFunc) error {
	session, err := rs.client.List(ctx, &contentapi.ListContentRequest{})
	if err != nil {
		return rewriteGRPCError(err)
	}

	for {
		msg, err := session.Recv()
		if err != nil {
			if err != io.EOF {
				return rewriteGRPCError(err)
			}

			break
		}

		for _, info := range msg.Info {
			if err := fn(content.Info{
				Digest:      info.Digest,
				Size:        info.Size_,
				CommittedAt: info.CommittedAt,
			}); err != nil {
				return err
			}
		}
	}

	return nil
}

func (rs *remoteStore) Delete(ctx context.Context, dgst digest.Digest) error {
	if _, err := rs.client.Delete(ctx, &contentapi.DeleteContentRequest{
		Digest: dgst,
	}); err != nil {
		return rewriteGRPCError(err)
	}

	return nil
}

func (rs *remoteStore) Reader(ctx context.Context, dgst digest.Digest) (io.ReadCloser, error) {
	client, err := rs.client.Read(ctx, &contentapi.ReadRequest{Digest: dgst})
	if err != nil {
		return nil, err
	}

	return &remoteReader{
		client: client,
	}, nil
}

func (rs *remoteStore) Status(ctx context.Context, re string) ([]content.Status, error) {
	resp, err := rs.client.Status(ctx, &contentapi.StatusRequest{
		Regexp: re,
	})
	if err != nil {
		return nil, rewriteGRPCError(err)
	}

	var statuses []content.Status
	for _, status := range resp.Statuses {
		statuses = append(statuses, content.Status{
			Ref:       status.Ref,
			StartedAt: status.StartedAt,
			UpdatedAt: status.UpdatedAt,
			Offset:    status.Offset,
			Total:     status.Total,
			Expected:  status.Expected,
		})
	}

	return statuses, nil
}

func (rs *remoteStore) Writer(ctx context.Context, ref string, size int64, expected digest.Digest) (content.Writer, error) {
	wrclient, offset, err := rs.negotiate(ctx, ref, size, expected)
	if err != nil {
		return nil, rewriteGRPCError(err)
	}

	return &remoteWriter{
		client: wrclient,
		offset: offset,
	}, nil
}

// Abort implements asynchronous abort. It starts a new write session on the ref l
func (rs *remoteStore) Abort(ctx context.Context, ref string) error {
	if _, err := rs.client.Abort(ctx, &contentapi.AbortRequest{
		Ref: ref,
	}); err != nil {
		return rewriteGRPCError(err)
	}

	return nil
}

func (rs *remoteStore) negotiate(ctx context.Context, ref string, size int64, expected digest.Digest) (contentapi.Content_WriteClient, int64, error) {
	wrclient, err := rs.client.Write(ctx)
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
