package images

import (
	"context"
	"encoding/json"
	"errors"
	"io/ioutil"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/log"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Image provides the model for how containerd views container images.
type Image struct {
	Name   string
	Target ocispec.Descriptor
	content.Provider
}

// TODO(stevvooe): Many of these functions make strong platform assumptions,
// which are untrue in a lot of cases. More refactoring must be done here to
// make this work in all cases.

// Config resolves the image configuration descriptor.
//
// The caller can then use the descriptor to resolve and process the
// configuration of the image.
func (image *Image) Config(ctx context.Context) (ocispec.Descriptor, error) {
	var configDesc ocispec.Descriptor
	return configDesc, Walk(ctx, HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		switch image.Target.MediaType {
		case MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest:
			rc, err := image.Provider.Reader(ctx, image.Target.Digest)
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

	}), image.Target)
}

// RootFS returns the unpacked diffids that make up and images rootfs.
//
// These are used to verify that a set of layers unpacked to the expected
// values.
func (image *Image) RootFS(ctx context.Context) ([]digest.Digest, error) {
	desc, err := image.Config(ctx)
	if err != nil {
		return nil, err
	}

	p, err := content.ReadBlob(ctx, image.Provider, desc.Digest)
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

// Size returns the total size of an image's packed resources.
func (image *Image) Size(ctx context.Context) (int64, error) {
	var size int64
	return size, Walk(ctx, HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		switch image.Target.MediaType {
		case MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest:
			size += desc.Size
			rc, err := image.Provider.Reader(ctx, image.Target.Digest)
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

			size += manifest.Config.Size

			for _, layer := range manifest.Layers {
				size += layer.Size
			}

			return nil, nil
		default:
			return nil, errors.New("unsupported type")
		}

	}), image.Target)
}
