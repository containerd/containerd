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
		return content.ErrExists
	case codes.NotFound:
		return content.ErrNotFound
	}

	return err
}

func serverErrorToGRPC(err error, id string) error {
	switch {
	case content.IsNotFound(err):
		return grpc.Errorf(codes.NotFound, "%v: not found", id)
	case content.IsExists(err):
		return grpc.Errorf(codes.AlreadyExists, "%v: exists", id)
	}

	return err
}
