package namespaces

import (
	"github.com/containerd/containerd/metadata"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func mapGRPCError(err error, id string) error {
	switch {
	case metadata.IsNotFound(err):
		return grpc.Errorf(codes.NotFound, "namespace %v not found", id)
	case metadata.IsExists(err):
		return grpc.Errorf(codes.AlreadyExists, "namespace %v already exists", id)
	case metadata.IsNotEmpty(err):
		return grpc.Errorf(codes.FailedPrecondition, "namespace %v must be empty", id)
	}

	return err
}

func rewriteGRPCError(err error) error {
	if err == nil {
		return err
	}

	switch grpc.Code(errors.Cause(err)) {
	case codes.AlreadyExists:
		return metadata.ErrExists
	case codes.NotFound:
		return metadata.ErrNotFound
	}

	return err
}
