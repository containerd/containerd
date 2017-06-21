package content

import (
	"github.com/containerd/containerd/content"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func rewriteGRPCError(err error) error {
	switch grpc.Code(errors.Cause(err)) {
	case codes.AlreadyExists:
		return content.ErrExists(grpc.ErrorDesc(err))
	case codes.NotFound:
		return content.ErrNotFound(grpc.ErrorDesc(err))
	case codes.Unavailable:
		return content.ErrLocked(grpc.ErrorDesc(err))
	}
	return err
}

func serverErrorToGRPC(err error, id string) error {
	switch {
	case content.IsNotFound(err):
		return grpc.Errorf(codes.NotFound, "%v: not found", id)
	case content.IsExists(err):
		return grpc.Errorf(codes.AlreadyExists, "%v: exists", id)
	case content.IsLocked(err):
		return grpc.Errorf(codes.Unavailable, "%v: locked", id)
	}

	return err
}
