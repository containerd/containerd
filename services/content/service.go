package content

import (
	"errors"
	"io"

	"github.com/docker/containerd"
	api "github.com/docker/containerd/api/services/content"
	"github.com/docker/containerd/content"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

type Service struct {
	store *content.Store
}

var _ api.ContentServer = &Service{}

func init() {
	containerd.Register("content-grpc", &containerd.Registration{
		Type: containerd.GRPCPlugin,
		Init: NewService,
	})
}

func NewService(ic *containerd.InitContext) (interface{}, error) {
	return &Service{
		store: ic.Store,
	}, nil
}

func (s *Service) Register(server *grpc.Server) error {
	api.RegisterContentServer(server, s)
	return nil
}

func (s *Service) Info(ctx context.Context, req *api.InfoRequest) (*api.InfoResponse, error) {
	if err := req.Digest.Validate(); err != nil {
		return nil, grpc.Errorf(codes.InvalidArgument, "%q failed validation", req.Digest)
	}

	bi, err := s.store.Info(req.Digest)
	if err != nil {
		return nil, maybeNotFoundGRPC(err, req.Digest.String())
	}

	return &api.InfoResponse{
		Digest:      req.Digest,
		Size_:       bi.Size,
		CommittedAt: bi.CommittedAt,
	}, nil
}

func (s *Service) Read(req *api.ReadRequest, session api.Content_ReadServer) error {
	if err := req.Digest.Validate(); err != nil {
		return grpc.Errorf(codes.InvalidArgument, "%v: %v", req.Digest, err)
	}

	oi, err := s.store.Info(req.Digest)
	if err != nil {
		return maybeNotFoundGRPC(err, req.Digest.String())
	}

	rc, err := s.store.Reader(session.Context(), req.Digest)
	if err != nil {
		return maybeNotFoundGRPC(err, req.Digest.String())
	}
	defer rc.Close() // TODO(stevvooe): Cache these file descriptors for performance.

	ra, ok := rc.(io.ReaderAt)
	if !ok {
		// TODO(stevvooe): Need to set this up to get correct behavior across
		// board. May change interface to store to just return ReaderAtCloser.
		// Possibly, we could just return io.ReaderAt and handle file
		// descriptors internally.
		return errors.New("content service only supports content stores that return ReaderAt")
	}

	var (
		offset = req.Offset
		size   = req.Size_

		// TODO(stevvooe): Using the global buffer pool. At 32KB, it is probably
		// little inefficient for work over a fast network. We can tune this later.
		p = content.BufPool.Get().([]byte)
	)
	defer content.BufPool.Put(p)

	if offset < 0 {
		offset = 0
	}

	if size <= 0 {
		size = oi.Size - offset
	}

	if offset+size > oi.Size {
		return grpc.Errorf(codes.OutOfRange, "read past object length %v bytes", oi.Size)
	}

	if _, err := io.CopyBuffer(
		&readResponseWriter{session: session},
		io.NewSectionReader(ra, offset, size), p); err != nil {
		return err
	}

	return nil
}

type readResponseWriter struct {
	offset  int64
	session api.Content_ReadServer
}

func (rw *readResponseWriter) Write(p []byte) (n int, err error) {
	if err := rw.session.Send(&api.ReadResponse{
		Offset: rw.offset,
		Data:   p,
	}); err != nil {
		return 0, err
	}

	rw.offset += int64(len(p))
	return len(p), nil
}

func (s *Service) Write(session api.Content_WriteServer) (err error) {
	var (
		ref string
		msg api.WriteResponse
		req *api.WriteRequest
	)

	defer func(msg *api.WriteResponse) {
		// pump through the last message if no error was encountered
		if err != nil {
			return
		}

		err = session.Send(msg)
	}(&msg)

	// handle the very first request!
	req, err = session.Recv()
	if err != nil {
		return err
	}

	ref = req.Ref
	if ref == "" {
		return grpc.Errorf(codes.InvalidArgument, "first message must have a reference")
	}

	// this action locks the writer for the session.
	wr, err := s.store.Writer(session.Context(), ref)
	if err != nil {
		return err
	}
	defer wr.Close()

	for {
		// TODO(stevvooe): We need to study this behavior in containerd a
		// little better to decide where to put this. We may be able to make
		// this determination elsewhere and avoid even creating the writer.
		//
		// Ideally, we just use the expected digest on commit to abandon the
		// cost of the move when they collide.
		if req.ExpectedDigest != "" {
			if _, err := s.store.Info(req.ExpectedDigest); err != nil {
				if !content.IsNotFound(err) {
					return err
				}

				return grpc.Errorf(codes.AlreadyExists, "blob with expected digest %v exists", req.ExpectedDigest)
			}
		}

		msg.Action = req.Action
		ws, err := wr.Status()
		if err != nil {
			return err
		}

		msg.Offset = ws.Offset
		msg.StartedAt = ws.StartedAt
		msg.UpdatedAt = ws.UpdatedAt

		switch req.Action {
		case api.WriteActionStat:
			msg.Digest = wr.Digest()
		case api.WriteActionWrite, api.WriteActionCommit:
			if req.Offset > 0 {
				// validate the offset if provided
				if req.Offset != ws.Offset {
					return grpc.Errorf(codes.OutOfRange, "write @%v must occur at current offset %v", req.Offset, ws.Offset)
				}
			}

			// issue the write if we actually have data.
			if len(req.Data) > 0 {
				// While this looks like we could use io.WriterAt here, because we
				// maintain the offset as append only, we just issue the write.
				n, err := wr.Write(req.Data)
				if err != nil {
					return err
				}

				if n != len(req.Data) {
					// TODO(stevvooe): Perhaps, we can recover this by including it
					// in the offset on the write return.
					return grpc.Errorf(codes.DataLoss, "wrote %v of %v bytes", n, len(req.Data))
				}

				msg.Offset += int64(n)
			}

			if req.Action == api.WriteActionCommit {
				return wr.Commit(req.ExpectedSize, req.ExpectedDigest)
			}
		case api.WriteActionAbort:
			return s.store.Abort(ref)
		}

		if err := session.Send(&msg); err != nil {
			return err
		}

		req, err = session.Recv()
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) Status(*api.StatusRequest, api.Content_StatusServer) error {
	return grpc.Errorf(codes.Unimplemented, "not implemented")
}

func maybeNotFoundGRPC(err error, id string) error {
	if content.IsNotFound(err) {
		return grpc.Errorf(codes.NotFound, "%v: not found", id)
	}

	return err
}
