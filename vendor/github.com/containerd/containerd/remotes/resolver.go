package remotes

import (
	"context"
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Resolver provides a remote based on a locator.
type Resolver interface {
	// Resolve attempts to resolve the reference into a name and descriptor.
	//
	// The argument `ref` should be a scheme-less URI representing the remote.
	// Structurally, it has a host and path. The "host" can be used to directly
	// reference a specific host or be matched against a specific handler.
	//
	// The returned name should be used to identify the referenced entity.
	// Dependending on the remote namespace, this may be immutable or mutable.
	// While the name may differ from ref, it should itself be a valid ref.
	//
	// If the resolution fails, an error will be returned.
	Resolve(ctx context.Context, ref string) (name string, desc ocispec.Descriptor, fetcher Fetcher, err error)
}

type Fetcher interface {
	// Fetch the resource identified by the descriptor.
	Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error)
}

// FetcherFunc allows package users to implement a Fetcher with just a
// function.
type FetcherFunc func(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error)

func (fn FetcherFunc) Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	return fn(ctx, desc)
}
