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

// Package docker provides a Docker compatible importer capable of
// importing both Docker and OCI formats.
package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/ioutil"
	"path"
	"strings"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/cri/pkg/util"
	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// V1Importer implements OCI Image Spec v1.
type V1Importer struct {
	// TODO: Add option to compress layers on ingest
}

var _ images.Importer = &V1Importer{}

// Import implements Importer.
func (oi *V1Importer) Import(ctx context.Context, store content.Store, reader io.Reader) (ocispec.Descriptor, error) {
	var (
		desc ocispec.Descriptor
		tr   = tar.NewReader(reader)

		mfsts         []manifestDotJSON
		symlinkLayers = make(map[string]string)             // key: filename (foobar/layer.tar), value: linkname (targetlayerid/layer.tar)
		layers        = make(map[string]ocispec.Descriptor) // key: filename (foobar/layer.tar)
		configs       = make(map[string]imageConfig)        // key: filename (foobar.json)

	)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		if hdr.Typeflag == tar.TypeSymlink && isLayerTar(hdr.Name) {
			linkname, err := followSymlinkLayer(hdr.Linkname)
			if err != nil {
				log.G(ctx).WithError(err).WithField("file", hdr.Name).Debugf("symlink to %s ignored", hdr.Linkname)
			} else {
				symlinkLayers[hdr.Name] = linkname
			}
			continue
		}

		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			log.G(ctx).WithField("file", hdr.Name).Debug("file type ignored")
			continue
		}
		hdrName := path.Clean(hdr.Name)
		if hdrName == "index.json" {
			if desc.Digest != "" {
				return ocispec.Descriptor{}, errors.New("duplicated index.json")
			}
			desc, err = onUntarIndexJSON(ctx, tr, store, hdr.Size)
			if err != nil {
				return ocispec.Descriptor{}, err
			}
		} else if strings.HasPrefix(hdrName, "blobs/") {
			if err := onUntarBlob(ctx, tr, store, hdrName, hdr.Size); err != nil {
				return ocispec.Descriptor{}, err
			}
		} else if hdrName == ocispec.ImageLayoutFile {
			// TODO Validate
		} else if hdr.Name == "manifest.json" {
			mfsts, err = onUntarManifestJSON(tr)
			if err != nil {
				return ocispec.Descriptor{}, errors.Wrapf(err, "untar manifest %q", hdr.Name)
			}
		} else if isLayerTar(hdr.Name) {
			desc, err := onUntarLayerTar(ctx, tr, store, hdr.Name, hdr.Size)
			if err != nil {
				return ocispec.Descriptor{}, errors.Wrapf(err, "untar layer %q", hdr.Name)
			}
			layers[hdr.Name] = desc
		} else if isDotJSON(hdr.Name) {
			c, err := onUntarDotJSON(ctx, tr, store, hdr.Name, hdr.Size)
			if err != nil {
				return ocispec.Descriptor{}, errors.Wrapf(err, "untar config %q", hdr.Name)
			}
			configs[hdr.Name] = c
		} else {
			log.G(ctx).WithField("file", hdr.Name).Debug("unknown file ignored")
		}

	}
	if desc.Digest != "" {
		return desc, nil
	}

	for name, linkname := range symlinkLayers {
		desc, ok := layers[linkname]
		if !ok {
			return ocispec.Descriptor{}, errors.Errorf("no target for symlink layer from %q to %q", name, linkname)
		}
		layers[name] = desc
	}

	var idx ocispec.Index
	for _, mfst := range mfsts {
		config, ok := configs[mfst.Config]
		if !ok {
			return ocispec.Descriptor{}, errors.Errorf("image config %q not found", mfst.Config)
		}

		layers, err := resolveLayers(mfst.Layers, layers)
		if err != nil {
			return ocispec.Descriptor{}, errors.Wrap(err, "failed to resolve layers")
		}

		manifest := ocispec.Manifest{
			Versioned: specs.Versioned{
				SchemaVersion: 2,
			},
			Config: config.desc,
			Layers: layers,
		}

		desc, err := writeManifest(ctx, store, manifest, config.img.Architecture, config.img.OS)
		if err != nil {
			return ocispec.Descriptor{}, errors.Wrap(err, "write docker manifest")
		}

		if len(mfst.RepoTags) == 0 {
			idx.Manifests = append(idx.Manifests, desc)
		} else {
			// Add descriptor per tag
			for _, ref := range mfst.RepoTags {
				msftdesc := desc

				// TODO: Replace this function to not depend on reference package
				normalized, err := util.NormalizeImageRef(ref)
				if err != nil {
					return ocispec.Descriptor{}, errors.Wrapf(err, "normalize image ref %q", ref)
				}

				msftdesc.Annotations = map[string]string{
					ocispec.AnnotationRefName: normalized.String(),
				}

				idx.Manifests = append(idx.Manifests, msftdesc)
			}
		}
	}

	return writeIndex(ctx, store, idx)
}

// RefTranslator creates a reference which only has a tag or verifies
// a full reference.
func RefTranslator(image string, checkPrefix bool) func(string) string {
	return func(ref string) string {
		// Check if ref is full reference
		if strings.ContainsAny(ref, "/:@") {
			// If not prefixed, don't include image
			if checkPrefix && !isImagePrefix(ref, image) {
				return ""
			}
			return ref
		}
		return image + ":" + ref
	}
}

func onUntarIndexJSON(ctx context.Context, r io.Reader, store content.Ingester, size int64) (ocispec.Descriptor, error) {
	b, err := ioutil.ReadAll(r)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(b),
		Size:      size,
	}
	if int64(len(b)) != size {
		return ocispec.Descriptor{}, errors.Errorf("size mismatch %d v %d", len(b), size)
	}

	if err := content.WriteBlob(ctx, store, "index-"+desc.Digest.String(), bytes.NewReader(b), desc); err != nil {
		return ocispec.Descriptor{}, err
	}

	return desc, err
}

func onUntarBlob(ctx context.Context, r io.Reader, store content.Ingester, name string, size int64) error {
	// name is like "blobs/sha256/deadbeef"
	split := strings.Split(name, "/")
	if len(split) != 3 {
		return errors.Errorf("unexpected name: %q", name)
	}
	algo := digest.Algorithm(split[1])
	if !algo.Available() {
		return errors.Errorf("unsupported algorithm: %s", algo)
	}
	dgst := digest.NewDigestFromHex(algo.String(), split[2])
	return content.WriteBlob(ctx, store, "blob-"+dgst.String(), r, ocispec.Descriptor{Size: size, Digest: dgst})
}

// manifestDotJSON is an entry in manifest.json.
type manifestDotJSON struct {
	Config   string
	RepoTags []string
	Layers   []string
	// Parent is unsupported
	Parent string
}

// isLayerTar returns true if name is like "foobar/layer.tar"
func isLayerTar(name string) bool {
	slashes := len(strings.Split(name, "/"))
	return slashes == 2 && strings.HasSuffix(name, "/layer.tar")
}

// followSymlinkLayer returns actual layer name of the symlink layer.
// It returns "foobar/layer.tar" if the name is like
// "../foobar/layer.tar", and returns error if the name
// is not in "../foobar/layer.tar" format.
func followSymlinkLayer(name string) (string, error) {
	parts := strings.Split(name, "/")
	if len(parts) != 3 || parts[0] != ".." {
		return "", errors.New("invalid symlink layer")
	}
	name = strings.TrimPrefix(name, "../")
	if !isLayerTar(name) {
		return "", errors.New("invalid layer tar")
	}
	return name, nil
}

// isDotJSON returns true if name is like "foobar.json"
func isDotJSON(name string) bool {
	slashes := len(strings.Split(name, "/"))
	return slashes == 1 && strings.HasSuffix(name, ".json")
}

func resolveLayers(layerIDs []string, layerIDMap map[string]ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	var layers []ocispec.Descriptor
	for _, f := range layerIDs {
		desc, ok := layerIDMap[f]
		if !ok {
			return nil, errors.Errorf("layer %q not found", f)
		}
		layers = append(layers, desc)
	}
	return layers, nil
}

func writeManifest(ctx context.Context, cs content.Ingester, manifest ocispec.Manifest, arch, os string) (ocispec.Descriptor, error) {
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}
	if err := content.WriteBlob(ctx, cs, "manifest-"+desc.Digest.String(), bytes.NewReader(manifestBytes), desc); err != nil {
		return ocispec.Descriptor{}, err
	}

	if arch != "" || os != "" {
		desc.Platform = &ocispec.Platform{
			Architecture: arch,
			OS:           os,
		}
	}
	return desc, nil
}

func writeIndex(ctx context.Context, cs content.Ingester, manifest ocispec.Index) (ocispec.Descriptor, error) {
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return ocispec.Descriptor{}, err
	}

	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}
	if err := content.WriteBlob(ctx, cs, "index-"+desc.Digest.String(), bytes.NewReader(manifestBytes), desc); err != nil {
		return ocispec.Descriptor{}, err
	}

	return desc, nil
}

func onUntarManifestJSON(r io.Reader) ([]manifestDotJSON, error) {
	// name: "manifest.json"
	b, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var mfsts []manifestDotJSON
	if err := json.Unmarshal(b, &mfsts); err != nil {
		return nil, err
	}
	return mfsts, nil
}

func onUntarLayerTar(ctx context.Context, r io.Reader, cs content.Ingester, name string, size int64) (ocispec.Descriptor, error) {
	// name is like "foobar/layer.tar" ( guaranteed by isLayerTar() )
	split := strings.Split(name, "/")
	// note: split[0] is not expected digest here
	cw, err := cs.Writer(ctx, content.WithRef("layer-"+split[0]), content.WithDescriptor(ocispec.Descriptor{Size: size}))
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	// TODO: support compression and committing with labels
	defer cw.Close()
	if err := content.Copy(ctx, cw, r, size, ""); err != nil {
		return ocispec.Descriptor{}, err
	}
	return ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Size:      size,
		Digest:    cw.Digest(),
	}, nil
}

type imageConfig struct {
	desc ocispec.Descriptor
	img  ocispec.Image
}

func onUntarDotJSON(ctx context.Context, r io.Reader, cs content.Ingester, name string, size int64) (imageConfig, error) {
	config := imageConfig{}
	config.desc.MediaType = ocispec.MediaTypeImageConfig
	config.desc.Size = size
	// name is like "foobar.json" ( guaranteed by is DotJSON() )
	split := strings.Split(name, ".")
	cw, err := cs.Writer(ctx, content.WithRef("config-"+split[0]), content.WithDescriptor(ocispec.Descriptor{Size: size}))
	if err != nil {
		return imageConfig{}, err
	}
	defer cw.Close()
	var buf bytes.Buffer
	tr := io.TeeReader(r, &buf)
	if err := content.Copy(ctx, cw, tr, size, ""); err != nil {
		return imageConfig{}, err
	}
	config.desc.Digest = cw.Digest()
	if err := json.Unmarshal(buf.Bytes(), &config.img); err != nil {
		return imageConfig{}, err
	}
	return config, nil
}

func isImagePrefix(s, prefix string) bool {
	if !strings.HasPrefix(s, prefix) {
		return false
	}
	if len(s) > len(prefix) {
		switch s[len(prefix)] {
		case '/', ':', '@':
			// Prevent matching partial namespaces
		default:
			return false
		}
	}
	return true
}
