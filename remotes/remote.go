package remotes

import (
	"context"
	"io"
)

type Remote interface {
	// Fetch the resource identified by id. The id is opaque to the remote, but
	// may typically be a tag or a digest.
	//
	// Hints are provided to give instruction on how the resource may be
	// fetched. They may provide information about the expected type or size.
	// They may be protocol specific or help a protocol to identify the most
	// efficient fetch methodology.
	//
	// Hints are the format of `<type>:<content>` where `<type>` is the type
	// of the hint and `<content>` can be pretty much anything. For example, a
	// media type hint would be the following:
	//
	// 	mediatype:application/vnd.docker.distribution.manifest.v2+json
	//
	// The following hint names are must be honored across all remote
	// implementations:
	//
	// 	size: specify the expected size in bytes
	// 	mediatype: specify the expected mediatype
	//
	// The caller should never expect the hints to be honored and should
	// validate that returned content is as expected. They are only provided to
	// help the remote retrieve the content.
	Fetch(ctx context.Context, id string, hints ...string) (io.ReadCloser, error)
}

type RemoteFunc func(context.Context, string, ...string) (io.ReadCloser, error)

func (fn RemoteFunc) Fetch(ctx context.Context, object string, hints ...string) (io.ReadCloser, error) {
	return fn(ctx, object, hints...)
}
