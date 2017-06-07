package remotes

import (
	"context"
	"fmt"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// MakeRef returns a unique reference for the descriptor. This reference can be
// used to lookup ongoing processes related to the descriptor. This function
// may look to the context to namespace the reference appropriately.
func MakeRefKey(ctx context.Context, desc ocispec.Descriptor) string {
	// TODO(stevvooe): Need better remote key selection here. Should be a
	// product of the context, which may include information about the ongoing
	// fetch process.
	switch desc.MediaType {
	case images.MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest,
		images.MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex:
		return "manifest-" + desc.Digest.String()
	case images.MediaTypeDockerSchema2Layer, images.MediaTypeDockerSchema2LayerGzip:
		return "layer-" + desc.Digest.String()
	case "application/vnd.docker.container.image.v1+json":
		return "config-" + desc.Digest.String()
	default:
		log.G(ctx).Warnf("reference for unknown type: %s", desc.MediaType)
		return "unknown-" + desc.Digest.String()
	}
}

// FetchHandler returns a handler that will fetch all content into the ingester
// discovered in a call to Dispatch. Use with ChildrenHandler to do a full
// recursive fetch.
func FetchHandler(ingester content.Ingester, fetcher Fetcher) images.HandlerFunc {
	return func(ctx context.Context, desc ocispec.Descriptor) (subdescs []ocispec.Descriptor, err error) {
		ctx = log.WithLogger(ctx, log.G(ctx).WithFields(logrus.Fields{
			"digest":    desc.Digest,
			"mediatype": desc.MediaType,
			"size":      desc.Size,
		}))

		switch desc.MediaType {
		case images.MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex:
			return nil, fmt.Errorf("%v not yet supported", desc.MediaType)
		default:
			err := fetch(ctx, ingester, fetcher, desc)
			return nil, err
		}
	}
}

func fetch(ctx context.Context, ingester content.Ingester, fetcher Fetcher, desc ocispec.Descriptor) error {
	log.G(ctx).Debug("fetch")
	if ip, ok := ingester.(content.InfoProvider); ok {
		if _, err := ip.Info(ctx, desc.Digest); err == nil {
			return nil
		}
	}
	ref := MakeRefKey(ctx, desc)

	cw, err := ingester.Writer(ctx, ref, desc.Size, desc.Digest)
	if err != nil {
		if !content.IsExists(err) {
			return err
		}

		return nil
	}
	defer cw.Close()

	rc, err := fetcher.Fetch(ctx, desc)
	if err != nil {
		return err
	}
	defer rc.Close()

	return content.Copy(cw, rc, desc.Size, desc.Digest)
}

func PushHandler(provider content.Provider, pusher Pusher) images.HandlerFunc {
	return func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		ctx = log.WithLogger(ctx, log.G(ctx).WithFields(logrus.Fields{
			"digest":    desc.Digest,
			"mediatype": desc.MediaType,
			"size":      desc.Size,
		}))

		log.G(ctx).Debug("push")
		r, err := provider.Reader(ctx, desc.Digest)
		if err != nil {
			return nil, err
		}
		defer r.Close()

		return nil, pusher.Push(ctx, desc, r)
	}
}
