/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package images

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images/encryption"
	encconfig "github.com/containerd/containerd/images/encryption/config"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/platforms"
	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// Image provides the model for how containerd views container images.
type Image struct {
	// Name of the image.
	//
	// To be pulled, it must be a reference compatible with resolvers.
	//
	// This field is required.
	Name string

	// Labels provide runtime decoration for the image record.
	//
	// There is no default behavior for how these labels are propagated. They
	// only decorate the static metadata object.
	//
	// This field is optional.
	Labels map[string]string

	// Target describes the root content for this image. Typically, this is
	// a manifest, index or manifest list.
	Target ocispec.Descriptor

	CreatedAt, UpdatedAt time.Time
}

// DeleteOptions provide options on image delete
type DeleteOptions struct {
	Synchronous bool
}

// DeleteOpt allows configuring a delete operation
type DeleteOpt func(context.Context, *DeleteOptions) error

// SynchronousDelete is used to indicate that an image deletion and removal of
// the image resources should occur synchronously before returning a result.
func SynchronousDelete() DeleteOpt {
	return func(ctx context.Context, o *DeleteOptions) error {
		o.Synchronous = true
		return nil
	}
}

// Store and interact with images
type Store interface {
	Get(ctx context.Context, name string) (Image, error)
	List(ctx context.Context, filters ...string) ([]Image, error)
	Create(ctx context.Context, image Image) (Image, error)

	// Update will replace the data in the store with the provided image. If
	// one or more fieldpaths are provided, only those fields will be updated.
	Update(ctx context.Context, image Image, fieldpaths ...string) (Image, error)

	Delete(ctx context.Context, name string, opts ...DeleteOpt) error
}

type cryptoOp int

const (
	cryptoOpEncrypt    cryptoOp = iota
	cryptoOpDecrypt             = iota
	cryptoOpUnwrapOnly          = iota
)

// TODO(stevvooe): Many of these functions make strong platform assumptions,
// which are untrue in a lot of cases. More refactoring must be done here to
// make this work in all cases.

// Config resolves the image configuration descriptor.
//
// The caller can then use the descriptor to resolve and process the
// configuration of the image.
func (image *Image) Config(ctx context.Context, provider content.Provider, platform platforms.MatchComparer) (ocispec.Descriptor, error) {
	return Config(ctx, provider, image.Target, platform)
}

// RootFS returns the unpacked diffids that make up and images rootfs.
//
// These are used to verify that a set of layers unpacked to the expected
// values.
func (image *Image) RootFS(ctx context.Context, provider content.Provider, platform platforms.MatchComparer) ([]digest.Digest, error) {
	desc, err := image.Config(ctx, provider, platform)
	if err != nil {
		return nil, err
	}
	return RootFS(ctx, provider, desc)
}

// Size returns the total size of an image's packed resources.
func (image *Image) Size(ctx context.Context, provider content.Provider, platform platforms.MatchComparer) (int64, error) {
	var size int64
	return size, Walk(ctx, Handlers(HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		if desc.Size < 0 {
			return nil, errors.Errorf("invalid size %v in %v (%v)", desc.Size, desc.Digest, desc.MediaType)
		}
		size += desc.Size
		return nil, nil
	}), FilterPlatforms(ChildrenHandler(provider), platform)), image.Target)
}

type platformManifest struct {
	p *ocispec.Platform
	m *ocispec.Manifest
}

// Manifest resolves a manifest from the image for the given platform.
//
// When a manifest descriptor inside of a manifest index does not have
// a platform defined, the platform from the image config is considered.
//
// If the descriptor points to a non-index manifest, then the manifest is
// unmarshalled and returned without considering the platform inside of the
// config.
//
// TODO(stevvooe): This violates the current platform agnostic approach to this
// package by returning a specific manifest type. We'll need to refactor this
// to return a manifest descriptor or decide that we want to bring the API in
// this direction because this abstraction is not needed.`
func Manifest(ctx context.Context, provider content.Provider, image ocispec.Descriptor, platform platforms.MatchComparer) (ocispec.Manifest, error) {
	var (
		m        []platformManifest
		wasIndex bool
	)

	if err := Walk(ctx, HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		switch desc.MediaType {
		case MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest:
			p, err := content.ReadBlob(ctx, provider, desc)
			if err != nil {
				return nil, err
			}

			var manifest ocispec.Manifest
			if err := json.Unmarshal(p, &manifest); err != nil {
				return nil, err
			}

			if desc.Digest != image.Digest && platform != nil {
				if desc.Platform != nil && !platform.Match(*desc.Platform) {
					return nil, nil
				}

				if desc.Platform == nil {
					p, err := content.ReadBlob(ctx, provider, manifest.Config)
					if err != nil {
						return nil, err
					}

					var image ocispec.Image
					if err := json.Unmarshal(p, &image); err != nil {
						return nil, err
					}

					if !platform.Match(platforms.Normalize(ocispec.Platform{OS: image.OS, Architecture: image.Architecture})) {
						return nil, nil
					}

				}
			}

			m = append(m, platformManifest{
				p: desc.Platform,
				m: &manifest,
			})

			return nil, nil
		case MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex:
			p, err := content.ReadBlob(ctx, provider, desc)
			if err != nil {
				return nil, err
			}

			var idx ocispec.Index
			if err := json.Unmarshal(p, &idx); err != nil {
				return nil, err
			}

			if platform == nil {
				return idx.Manifests, nil
			}

			var descs []ocispec.Descriptor
			for _, d := range idx.Manifests {
				if d.Platform == nil || platform.Match(*d.Platform) {
					descs = append(descs, d)
				}
			}

			wasIndex = true

			return descs, nil

		}
		return nil, errors.Wrapf(errdefs.ErrNotFound, "unexpected media type %v for %v", desc.MediaType, desc.Digest)
	}), image); err != nil {
		return ocispec.Manifest{}, err
	}

	if len(m) == 0 {
		err := errors.Wrapf(errdefs.ErrNotFound, "manifest %v", image.Digest)
		if wasIndex {
			err = errors.Wrapf(errdefs.ErrNotFound, "no match for platform in manifest %v", image.Digest)
		}
		return ocispec.Manifest{}, err
	}

	sort.SliceStable(m, func(i, j int) bool {
		if m[i].p == nil {
			return false
		}
		if m[j].p == nil {
			return true
		}
		return platform.Less(*m[i].p, *m[j].p)
	})

	return *m[0].m, nil
}

// Config resolves the image configuration descriptor using a content provided
// to resolve child resources on the image.
//
// The caller can then use the descriptor to resolve and process the
// configuration of the image.
func Config(ctx context.Context, provider content.Provider, image ocispec.Descriptor, platform platforms.MatchComparer) (ocispec.Descriptor, error) {
	manifest, err := Manifest(ctx, provider, image, platform)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return manifest.Config, err
}

// Platforms returns one or more platforms supported by the image.
func Platforms(ctx context.Context, provider content.Provider, image ocispec.Descriptor) ([]ocispec.Platform, error) {
	var platformSpecs []ocispec.Platform
	return platformSpecs, Walk(ctx, Handlers(HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		if desc.Platform != nil {
			platformSpecs = append(platformSpecs, *desc.Platform)
			return nil, ErrSkipDesc
		}

		switch desc.MediaType {
		case MediaTypeDockerSchema2Config, ocispec.MediaTypeImageConfig:
			p, err := content.ReadBlob(ctx, provider, desc)
			if err != nil {
				return nil, err
			}

			var image ocispec.Image
			if err := json.Unmarshal(p, &image); err != nil {
				return nil, err
			}

			platformSpecs = append(platformSpecs,
				platforms.Normalize(ocispec.Platform{OS: image.OS, Architecture: image.Architecture}))
		}
		return nil, nil
	}), ChildrenHandler(provider)), image)
}

// Check returns nil if the all components of an image are available in the
// provider for the specified platform.
//
// If available is true, the caller can assume that required represents the
// complete set of content required for the image.
//
// missing will have the components that are part of required but not avaiiable
// in the provider.
//
// If there is a problem resolving content, an error will be returned.
func Check(ctx context.Context, provider content.Provider, image ocispec.Descriptor, platform platforms.MatchComparer) (available bool, required, present, missing []ocispec.Descriptor, err error) {
	mfst, err := Manifest(ctx, provider, image, platform)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return false, []ocispec.Descriptor{image}, nil, []ocispec.Descriptor{image}, nil
		}

		return false, nil, nil, nil, errors.Wrapf(err, "failed to check image %v", image.Digest)
	}

	// TODO(stevvooe): It is possible that referenced conponents could have
	// children, but this is rare. For now, we ignore this and only verify
	// that manifest components are present.
	required = append([]ocispec.Descriptor{mfst.Config}, mfst.Layers...)

	for _, desc := range required {
		ra, err := provider.ReaderAt(ctx, desc)
		if err != nil {
			if errdefs.IsNotFound(err) {
				missing = append(missing, desc)
				continue
			} else {
				return false, nil, nil, nil, errors.Wrapf(err, "failed to check image %v", desc.Digest)
			}
		}
		ra.Close()
		present = append(present, desc)

	}

	return true, required, present, missing, nil
}

// Children returns the immediate children of content described by the descriptor.
func Children(ctx context.Context, provider content.Provider, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	var descs []ocispec.Descriptor
	switch desc.MediaType {
	case MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest:
		p, err := content.ReadBlob(ctx, provider, desc)
		if err != nil {
			return nil, err
		}

		// TODO(stevvooe): We just assume oci manifest, for now. There may be
		// subtle differences from the docker version.
		var manifest ocispec.Manifest
		if err := json.Unmarshal(p, &manifest); err != nil {
			return nil, err
		}

		descs = append(descs, manifest.Config)
		descs = append(descs, manifest.Layers...)
	case MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex:
		p, err := content.ReadBlob(ctx, provider, desc)
		if err != nil {
			return nil, err
		}

		var index ocispec.Index
		if err := json.Unmarshal(p, &index); err != nil {
			return nil, err
		}

		descs = append(descs, index.Manifests...)
	case MediaTypeDockerSchema2Layer, MediaTypeDockerSchema2LayerGzip,
		MediaTypeDockerSchema2LayerEnc, MediaTypeDockerSchema2LayerGzipEnc,
		MediaTypeDockerSchema2LayerForeign, MediaTypeDockerSchema2LayerForeignGzip,
		MediaTypeDockerSchema2Config, ocispec.MediaTypeImageConfig,
		ocispec.MediaTypeImageLayer, ocispec.MediaTypeImageLayerGzip,
		ocispec.MediaTypeImageLayerNonDistributable, ocispec.MediaTypeImageLayerNonDistributableGzip,
		MediaTypeContainerd1Checkpoint, MediaTypeContainerd1CheckpointConfig:
		// childless data types.
		return nil, nil
	default:
		log.G(ctx).Warnf("encountered unknown type %v; children may not be fetched", desc.MediaType)
	}

	return descs, nil
}

// RootFS returns the unpacked diffids that make up and images rootfs.
//
// These are used to verify that a set of layers unpacked to the expected
// values.
func RootFS(ctx context.Context, provider content.Provider, configDesc ocispec.Descriptor) ([]digest.Digest, error) {
	p, err := content.ReadBlob(ctx, provider, configDesc)
	if err != nil {
		return nil, err
	}

	var config ocispec.Image
	if err := json.Unmarshal(p, &config); err != nil {
		return nil, err
	}
	return config.RootFS.DiffIDs, nil
}

// IsEncryptedDiff returns true if mediaType is a known encrypted media type.
func IsEncryptedDiff(ctx context.Context, mediaType string) bool {
	switch mediaType {
	case MediaTypeDockerSchema2LayerGzipEnc, MediaTypeDockerSchema2LayerEnc:
		return true
	}
	return false
}

// HasEncryptedLayer returns true if any LayerInfo indicates that the layer is encrypted
func HasEncryptedLayer(ctx context.Context, layerInfos []encryption.LayerInfo) bool {
	for i := 0; i < len(layerInfos); i++ {
		if IsEncryptedDiff(ctx, layerInfos[i].Descriptor.MediaType) {
			return true
		}
	}
	return false
}

// IsCompressedDiff returns true if mediaType is a known compressed diff media type.
// It returns false if the media type is a diff, but not compressed. If the media type
// is not a known diff type, it returns errdefs.ErrNotImplemented
func IsCompressedDiff(ctx context.Context, mediaType string) (bool, error) {
	switch mediaType {
	case ocispec.MediaTypeImageLayer, MediaTypeDockerSchema2Layer:
	case ocispec.MediaTypeImageLayerGzip, MediaTypeDockerSchema2LayerGzip, MediaTypeDockerSchema2LayerGzipEnc:
		return true, nil
	default:
		// Still apply all generic media types *.tar[.+]gzip and *.tar
		if strings.HasSuffix(mediaType, ".tar.gzip") || strings.HasSuffix(mediaType, ".tar+gzip") {
			return true, nil
		} else if !strings.HasSuffix(mediaType, ".tar") {
			return false, errdefs.ErrNotImplemented
		}
	}
	return false, nil
}

// encryptLayer encrypts the layer using the CryptoConfig and creates a new OCI Descriptor.
// A call to this function may also only manipulate the wrapped keys list.
// The caller is expected to store the returned encrypted data and OCI Descriptor
func encryptLayer(cc *encconfig.CryptoConfig, dataReader content.ReaderAt, desc ocispec.Descriptor) (ocispec.Descriptor, content.ReaderDigester, error) {
	var (
		size int64
		d    digest.Digest
		err  error
	)

	encLayerReader, annotations, err := encryption.EncryptLayer(cc.Ec, dataReader, desc)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}

	// were data touched ?
	if encLayerReader != nil {
		size = 0
		d = encLayerReader.Digest()
	} else {
		size = desc.Size
		d = desc.Digest
	}

	newDesc := ocispec.Descriptor{
		Digest:      d,
		Size:        size,
		Platform:    desc.Platform,
		Annotations: annotations,
	}

	switch desc.MediaType {
	case MediaTypeDockerSchema2LayerGzip:
		newDesc.MediaType = MediaTypeDockerSchema2LayerGzipEnc
	case MediaTypeDockerSchema2Layer:
		newDesc.MediaType = MediaTypeDockerSchema2LayerEnc
	case MediaTypeDockerSchema2LayerGzipEnc:
		newDesc.MediaType = MediaTypeDockerSchema2LayerGzipEnc
	case MediaTypeDockerSchema2LayerEnc:
		newDesc.MediaType = MediaTypeDockerSchema2LayerEnc

		// TODO: Mediatypes to be added in ocispec
	case ocispec.MediaTypeImageLayerGzip:
		newDesc.MediaType = MediaTypeDockerSchema2LayerGzipEnc
	case ocispec.MediaTypeImageLayer:
		newDesc.MediaType = MediaTypeDockerSchema2LayerEnc

	default:
		return ocispec.Descriptor{}, nil, errors.Errorf("Encryption: unsupporter layer MediaType: %s\n", desc.MediaType)
	}

	return newDesc, encLayerReader, nil
}

// DecryptBlob decrypts the layer blob using the CryptoConfig and creates a new OCI Descriptor.
// The caller is expected to store the returned plain data and OCI Descriptor
func DecryptBlob(cc *encconfig.CryptoConfig, dataReader content.ReaderAt, desc ocispec.Descriptor, unwrapOnly bool) (ocispec.Descriptor, io.Reader, error) {
	if cc == nil {
		return ocispec.Descriptor{}, nil, errors.Wrapf(errdefs.ErrInvalidArgument, "CryptoConfig must not be nil")
	}
	resultReader, err := encryption.DecryptLayer(cc.Dc, dataReader, desc, unwrapOnly)
	if err != nil || unwrapOnly {
		return ocispec.Descriptor{}, nil, err
	}

	newDesc := ocispec.Descriptor{
		Digest:   resultReader.Digest(),
		Size:     0,
		Platform: desc.Platform,
	}

	switch desc.MediaType {
	case MediaTypeDockerSchema2LayerGzipEnc:
		newDesc.MediaType = MediaTypeDockerSchema2LayerGzip
	case MediaTypeDockerSchema2LayerEnc:
		newDesc.MediaType = MediaTypeDockerSchema2Layer
	default:
		return ocispec.Descriptor{}, nil, errors.Errorf("Decryption: unsupporter layer MediaType: %s\n", desc.MediaType)
	}
	return newDesc, resultReader, nil
}

// decryptLayer decrypts the layer using the CryptoConfig and creates a new OCI Descriptor.
// The caller is expected to store the returned plain data and OCI Descriptor
func decryptLayer(cc *encconfig.CryptoConfig, dataReader content.ReaderAt, desc ocispec.Descriptor, unwrapOnly bool) (ocispec.Descriptor, content.ReaderDigester, error) {
	resultReader, err := encryption.DecryptLayer(cc.Dc, dataReader, desc, unwrapOnly)
	if err != nil || unwrapOnly {
		return ocispec.Descriptor{}, nil, err
	}

	newDesc := ocispec.Descriptor{
		Digest:   resultReader.Digest(),
		Size:     0,
		Platform: desc.Platform,
	}

	switch desc.MediaType {
	case MediaTypeDockerSchema2LayerGzipEnc:
		newDesc.MediaType = MediaTypeDockerSchema2LayerGzip
	case MediaTypeDockerSchema2LayerEnc:
		newDesc.MediaType = MediaTypeDockerSchema2Layer
	default:
		return ocispec.Descriptor{}, nil, errors.Errorf("Decryption: unsupporter layer MediaType: %s\n", desc.MediaType)
	}
	return newDesc, resultReader, nil
}

// cryptLayer handles the changes due to encryption or decryption of a layer
func cryptLayer(ctx context.Context, cs content.Store, desc ocispec.Descriptor, cc *encconfig.CryptoConfig, cryptoOp cryptoOp) (ocispec.Descriptor, error) {
	var (
		resultReader content.ReaderDigester
		newDesc      ocispec.Descriptor
	)

	dataReader, err := cs.ReaderAt(ctx, desc)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer dataReader.Close()

	if cryptoOp == cryptoOpEncrypt {
		newDesc, resultReader, err = encryptLayer(cc, dataReader, desc)
	} else {
		newDesc, resultReader, err = decryptLayer(cc, dataReader, desc, cryptoOp == cryptoOpUnwrapOnly)
	}
	if err != nil || cryptoOp == cryptoOpUnwrapOnly {
		return ocispec.Descriptor{}, err
	}
	// some operations, such as changing recipients, may not touch the layer at all
	if resultReader != nil {
		newDesc.Size, err = content.WriteLayer(ctx, cs, resultReader, newDesc)
	}
	return newDesc, err
}

// isDecriptorALayer determines whether the given Descriptor describes an image layer
func isDescriptorALayer(desc ocispec.Descriptor) bool {
	switch desc.MediaType {
	case MediaTypeDockerSchema2LayerGzip, MediaTypeDockerSchema2Layer,
		MediaTypeDockerSchema2LayerGzipEnc, MediaTypeDockerSchema2LayerEnc:
		return true
	}
	return false
}

// countLayers counts the number of layer OCI descriptors in the given array
func countLayers(desc []ocispec.Descriptor) int32 {
	c := int32(0)

	for _, d := range desc {
		if isDescriptorALayer(d) {
			c = c + 1
		}
	}
	return c
}

// isUserSelectedLayer checks whether the a layer is user selected given its number
// A layer can be described with its (positive) index number or its negative number, which
// is counted relative to the topmost one (-1)
func isUserSelectedLayer(layerIndex, layersTotal int32, layers []int32) bool {
	if len(layers) == 0 {
		// convenience for the user; none given means 'all'
		return true
	}
	negNumber := layerIndex - layersTotal

	for _, l := range layers {
		if l == negNumber || l == layerIndex {
			return true
		}
	}
	return false
}

// isUserSelectedPlatform determines whether the platform matches one in
// the array of user provided platforms
func isUserSelectedPlatform(platform *ocispec.Platform, platformList []ocispec.Platform) bool {
	if len(platformList) == 0 {
		// convenience for the user; none given means 'all'
		return true
	}
	matcher := platforms.NewMatcher(*platform)

	for _, platform := range platformList {
		if matcher.Match(platform) {
			return true
		}
	}
	return false
}

// Encrypt or decrypt all the Children of a given descriptor
func cryptChildren(ctx context.Context, cs content.Store, desc ocispec.Descriptor, cc *encconfig.CryptoConfig, lf *encryption.LayerFilter, cryptoOp cryptoOp, thisPlatform *ocispec.Platform) (ocispec.Descriptor, bool, error) {
	layerIndex := int32(0)

	children, err := Children(ctx, cs, desc)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return desc, false, nil
		}
		return ocispec.Descriptor{}, false, err
	}

	layersTotal := countLayers(children)

	var newLayers []ocispec.Descriptor
	var config ocispec.Descriptor
	modified := false

	for _, child := range children {
		// we only encrypt child layers and have to update their parents if encyrption happened
		switch child.MediaType {
		case MediaTypeDockerSchema2Config, ocispec.MediaTypeImageConfig:
			config = child
		case MediaTypeDockerSchema2LayerGzip, MediaTypeDockerSchema2Layer,
			ocispec.MediaTypeImageLayerGzip, ocispec.MediaTypeImageLayer:
			if cryptoOp == cryptoOpEncrypt && isUserSelectedLayer(layerIndex, layersTotal, lf.Layers) && isUserSelectedPlatform(thisPlatform, lf.Platforms) {
				nl, err := cryptLayer(ctx, cs, child, cc, cryptoOp)
				if err != nil {
					return ocispec.Descriptor{}, false, err
				}
				modified = true
				newLayers = append(newLayers, nl)
			} else {
				newLayers = append(newLayers, child)
			}
			layerIndex = layerIndex + 1
		case MediaTypeDockerSchema2LayerGzipEnc, MediaTypeDockerSchema2LayerEnc:
			// this one can be decrypted but also its recpients list changed
			if isUserSelectedLayer(layerIndex, layersTotal, lf.Layers) && isUserSelectedPlatform(thisPlatform, lf.Platforms) {
				nl, err := cryptLayer(ctx, cs, child, cc, cryptoOp)
				if err != nil || cryptoOp == cryptoOpUnwrapOnly {
					return ocispec.Descriptor{}, false, err
				}
				modified = true
				newLayers = append(newLayers, nl)
			} else {
				newLayers = append(newLayers, child)
			}
			layerIndex = layerIndex + 1
		case MediaTypeDockerSchema2LayerForeign, MediaTypeDockerSchema2LayerForeignGzip:
			/* never encrypt/decrypt */
			newLayers = append(newLayers, child)
			layerIndex = layerIndex + 1
		default:
			return ocispec.Descriptor{}, false, errors.Errorf("Bad/unhandled MediaType %s in encryptChildren\n", child.MediaType)
		}
	}

	if modified && len(newLayers) > 0 {
		newManifest := ocispec.Manifest{
			Versioned: specs.Versioned{
				SchemaVersion: 2,
			},
			Config: config,
			Layers: newLayers,
		}

		mb, err := json.MarshalIndent(newManifest, "", "   ")
		if err != nil {
			return ocispec.Descriptor{}, false, errors.Wrap(err, "failed to marshal image")
		}

		newDesc := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageManifest,
			Size:      int64(len(mb)),
			Digest:    digest.Canonical.FromBytes(mb),
			Platform:  desc.Platform,
		}

		labels := map[string]string{}
		labels["containerd.io/gc.ref.content.0"] = newManifest.Config.Digest.String()
		for i, ch := range newManifest.Layers {
			labels[fmt.Sprintf("containerd.io/gc.ref.content.%d", i+1)] = ch.Digest.String()
		}

		ref := fmt.Sprintf("manifest-%s", newDesc.Digest.String())
		if err := content.WriteBlob(ctx, cs, ref, bytes.NewReader(mb), newDesc, content.WithLabels(labels)); err != nil {
			return ocispec.Descriptor{}, false, errors.Wrap(err, "failed to write config")
		}
		return newDesc, true, nil
	}

	return desc, modified, nil
}

// cryptManifest encrypts or decrypts the children of a top level manifest
func cryptManifest(ctx context.Context, cs content.Store, desc ocispec.Descriptor, cc *encconfig.CryptoConfig, lf *encryption.LayerFilter, cryptoOp cryptoOp) (ocispec.Descriptor, bool, error) {
	p, err := content.ReadBlob(ctx, cs, desc)
	if err != nil {
		return ocispec.Descriptor{}, false, err
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(p, &manifest); err != nil {
		return ocispec.Descriptor{}, false, err
	}
	platform := platforms.DefaultSpec()
	newDesc, modified, err := cryptChildren(ctx, cs, desc, cc, lf, cryptoOp, &platform)
	if err != nil || cryptoOp == cryptoOpUnwrapOnly {
		return ocispec.Descriptor{}, false, err
	}
	return newDesc, modified, nil
}

// cryptManifestList encrypts or decrypts the children of a top level manifest list
func cryptManifestList(ctx context.Context, cs content.Store, desc ocispec.Descriptor, cc *encconfig.CryptoConfig, lf *encryption.LayerFilter, cryptoOp cryptoOp) (ocispec.Descriptor, bool, error) {
	// read the index; if any layer is encrypted and any manifests change we will need to rewrite it
	b, err := content.ReadBlob(ctx, cs, desc)
	if err != nil {
		return ocispec.Descriptor{}, false, err
	}

	var index ocispec.Index
	if err := json.Unmarshal(b, &index); err != nil {
		return ocispec.Descriptor{}, false, err
	}

	var newManifests []ocispec.Descriptor
	modified := false
	for _, manifest := range index.Manifests {
		newManifest, m, err := cryptChildren(ctx, cs, manifest, cc, lf, cryptoOp, manifest.Platform)
		if err != nil || cryptoOp == cryptoOpUnwrapOnly {
			return ocispec.Descriptor{}, false, err
		}
		if m {
			modified = true
		}
		newManifests = append(newManifests, newManifest)
	}

	if modified {
		// we need to update the index

		newIndex := ocispec.Index{
			Versioned: index.Versioned,
			Manifests: newManifests,
		}

		mb, err := json.MarshalIndent(newIndex, "", "   ")
		if err != nil {
			return ocispec.Descriptor{}, false, errors.Wrap(err, "failed to marshal index")
		}

		newDesc := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageIndex,
			Size:      int64(len(mb)),
			Digest:    digest.Canonical.FromBytes(mb),
		}

		labels := map[string]string{}
		for i, m := range newIndex.Manifests {
			labels[fmt.Sprintf("containerd.io/gc.ref.content.%d", i)] = m.Digest.String()
		}

		ref := fmt.Sprintf("index-%s", newDesc.Digest.String())
		if err := content.WriteBlob(ctx, cs, ref, bytes.NewReader(mb), newDesc, content.WithLabels(labels)); err != nil {
			return ocispec.Descriptor{}, false, errors.Wrap(err, "failed to write index")
		}
		return newDesc, true, nil
	}

	return desc, false, nil
}

// cryptImage is the dispatcher to encrypt/decrypt an image; it accepts either an OCI descriptor
// representing a manifest list or a single manifest
func cryptImage(ctx context.Context, cs content.Store, desc ocispec.Descriptor, cc *encconfig.CryptoConfig, lf *encryption.LayerFilter, cryptoOp cryptoOp) (ocispec.Descriptor, bool, error) {
	if cc == nil {
		return ocispec.Descriptor{}, false, errors.Wrapf(errdefs.ErrInvalidArgument, "CryptoConfig must not be nil")
	}
	switch desc.MediaType {
	case ocispec.MediaTypeImageIndex, MediaTypeDockerSchema2ManifestList:
		return cryptManifestList(ctx, cs, desc, cc, lf, cryptoOp)
	case ocispec.MediaTypeImageManifest, MediaTypeDockerSchema2Manifest:
		return cryptManifest(ctx, cs, desc, cc, lf, cryptoOp)
	default:
		return ocispec.Descriptor{}, false, errors.Errorf("CryptImage: Unhandled media type: %s", desc.MediaType)
	}
}

// EncryptImage encrypts an image; it accepts either an OCI descriptor representing a manifest list or a single manifest
func EncryptImage(ctx context.Context, cs content.Store, desc ocispec.Descriptor, cc *encconfig.CryptoConfig, lf *encryption.LayerFilter) (ocispec.Descriptor, bool, error) {
	return cryptImage(ctx, cs, desc, cc, lf, cryptoOpEncrypt)
}

// DecryptImage decrypts an image; it accepts either an OCI descriptor representing a manifest list or a single manifest
func DecryptImage(ctx context.Context, cs content.Store, desc ocispec.Descriptor, cc *encconfig.CryptoConfig, lf *encryption.LayerFilter) (ocispec.Descriptor, bool, error) {
	return cryptImage(ctx, cs, desc, cc, lf, cryptoOpDecrypt)
}

// CheckAuthorization checks whether a user has the right keys to be allowed to access an image (every layer)
// It takes decrypting of the layers only as far as decrypting the asymmetrically encrypted data
// The decryption is only done for the current platform
func CheckAuthorization(ctx context.Context, cs content.Store, desc ocispec.Descriptor, dc *encconfig.DecryptConfig) error {
	cc := encconfig.CryptoConfig{
		Dc: dc,
	}
	lf := encryption.LayerFilter{
		Platforms: []ocispec.Platform{platforms.DefaultSpec()},
	}
	_, _, err := cryptImage(ctx, cs, desc, &cc, &lf, cryptoOpUnwrapOnly)
	if err != nil {
		return errors.Wrapf(err, "You are not authorized to used this image")
	}
	return nil
}

// GetImageLayerInfo gets the image key Ids necessary for decrypting an image
// We determine the KeyIds starting with  the given OCI Decriptor, recursing to lower-level descriptors
// until we get them from the layer descriptors
func GetImageLayerInfo(ctx context.Context, cs content.Store, desc ocispec.Descriptor, lf *encryption.LayerFilter, layerIndex int32) ([]encryption.LayerInfo, error) {
	ds := platforms.DefaultSpec()
	return getImageLayerInfo(ctx, cs, desc, lf, layerIndex, &ds)
}

// getImageLayerInfo is the recursive version of GetImageLayerInfo that takes the platform
// as additional parameter
func getImageLayerInfo(ctx context.Context, cs content.Store, desc ocispec.Descriptor, lf *encryption.LayerFilter, layerIndex int32, platform *ocispec.Platform) ([]encryption.LayerInfo, error) {
	var (
		lis []encryption.LayerInfo
		tmp []encryption.LayerInfo
	)

	switch desc.MediaType {
	case MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex,
		MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest:
		children, err := Children(ctx, cs, desc)
		if desc.Platform != nil {
			if !isUserSelectedPlatform(desc.Platform, lf.Platforms) {
				return []encryption.LayerInfo{}, nil
			}
			platform = desc.Platform
		}
		if err != nil {
			if errdefs.IsNotFound(err) {
				return []encryption.LayerInfo{}, nil
			}
			return []encryption.LayerInfo{}, err
		}

		layersTotal := countLayers(children)
		layerIndex := int32(-1)

		for _, child := range children {
			if isDescriptorALayer(child) {
				layerIndex = layerIndex + 1
				if isUserSelectedLayer(layerIndex, layersTotal, lf.Layers) {
					tmp, err = getImageLayerInfo(ctx, cs, child, lf, layerIndex, platform)
				} else {
					continue
				}
			} else {
				tmp, err = GetImageLayerInfo(ctx, cs, child, lf, -1)
			}
			if err != nil {
				return []encryption.LayerInfo{}, err
			}

			lis = append(lis, tmp...)
		}
	case MediaTypeDockerSchema2Layer, MediaTypeDockerSchema2LayerGzip:
		li := encryption.LayerInfo{
			Index: uint32(layerIndex),
			Descriptor: ocispec.Descriptor{
				Annotations: make(map[string]string),
				MediaType:   desc.MediaType,
				Digest:      desc.Digest,
				Size:        desc.Size,
				Platform:    platform,
			},
		}
		lis = append(lis, li)
	case MediaTypeDockerSchema2Config, ocispec.MediaTypeImageConfig:
	case MediaTypeDockerSchema2LayerEnc, MediaTypeDockerSchema2LayerGzipEnc:
		li := encryption.LayerInfo{
			Index: uint32(layerIndex),
			Descriptor: ocispec.Descriptor{
				Annotations: desc.Annotations,
				MediaType:   desc.MediaType,
				Digest:      desc.Digest,
				Size:        desc.Size,
				Platform:    platform,
			},
		}
		lis = append(lis, li)
	default:
		return []encryption.LayerInfo{}, errors.Wrapf(errdefs.ErrInvalidArgument, "GetImageLayerInfo: Unhandled media type %s", desc.MediaType)
	}

	return lis, nil
}
