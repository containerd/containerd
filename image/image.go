package image

import (
	"context"
	"encoding/json"
	"errors"
	"io/ioutil"

	"github.com/docker/containerd/content"
	"github.com/docker/containerd/log"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Image provides the model for how containerd views container images.
type Image struct {
	Name       string
	Descriptor ocispec.Descriptor
}

// TODO(stevvooe): Many of these functions make strong platform assumptions,
// which are untrue in a lot of cases. More refactoring must be done here to
// make this work in all cases.

// Config resolves the image configuration descriptor.
//
// The caller can then use the descriptor to resolve and process the
// configuration of the image.
func (image *Image) Config(ctx context.Context, provider content.Provider) (ocispec.Descriptor, error) {
	var configDesc ocispec.Descriptor
	return configDesc, Walk(ctx, HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		switch image.Descriptor.MediaType {
		case MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest:
			rc, err := provider.Reader(ctx, image.Descriptor.Digest)
			if err != nil {
				return nil, err
			}
			defer rc.Close()

			p, err := ioutil.ReadAll(rc)
			if err != nil {
				return nil, err
			}

			var manifest ocispec.Manifest
			if err := json.Unmarshal(p, &manifest); err != nil {
				return nil, err
			}

			configDesc = manifest.Config

			return nil, nil
		default:
			return nil, errors.New("could not resolve config")
		}

	}), image.Descriptor)
}

// RootFS returns the unpacked diffids that make up and images rootfs.
//
// These are used to verify that a set of layers unpacked to the expected
// values.
func (image *Image) RootFS(ctx context.Context, provider content.Provider) ([]digest.Digest, error) {
	desc, err := image.Config(ctx, provider)
	if err != nil {
		return nil, err
	}

	p, err := content.ReadBlob(ctx, provider, desc.Digest)
	if err != nil {
		log.G(ctx).Fatal(err)
	}

	var config ocispec.Image
	if err := json.Unmarshal(p, &config); err != nil {
		log.G(ctx).Fatal(err)
	}

	// TODO(stevvooe): Remove this bit when OCI structure uses correct type for
	// rootfs.DiffIDs.
	var diffIDs []digest.Digest
	for _, diffID := range config.RootFS.DiffIDs {
		diffIDs = append(diffIDs, digest.Digest(diffID))
	}

	return diffIDs, nil
}
