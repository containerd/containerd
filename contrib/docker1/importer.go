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

package docker1

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"path"
	"strings"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// Importer implements Docker Image Spec v1.1.
//
// FIXME(AkihiroSuda): current implementation restrictions:
// - An image MUST have `manifest.json`. `repositories` file in Docker Image Spec v1.0 is not supported
// - implicit file name convention is assumed (e.g. deadbeef/layer.tar)
type Importer struct {
	// If Conservative is true, the normalized reference can only be either tagged or digested.
	// For reference contains both tag and digest, the function returns digested reference, e.g.
	// docker.io/library/busybox:latest@sha256:7cc4b5aefd1d0cadf8d97d4350462ba51c694ebca145b08d7d41b41acc8db5aa
	// will be normalized as
	// docker.io/library/busybox@sha256:7cc4b5aefd1d0cadf8d97d4350462ba51c694ebca145b08d7d41b41acc8db5aa.
	Conservative bool
	// TODO: support adding labels to all objects
}

var _ images.Importer = &Importer{}

// isLayerTar returns true if name is like "deadbeeddeadbeef/layer.tar"
func isLayerTar(name string) bool {
	slashes := len(strings.Split(name, "/"))
	return slashes == 2 && strings.HasSuffix(name, "/layer.tar")
}

// isDotJSON returns true if name is like "deadbeefdeadbeef.json"
func isDotJSON(name string) bool {
	slashes := len(strings.Split(name, "/"))
	return slashes == 1 && strings.HasSuffix(name, ".json")
}

type imageConfig struct {
	desc ocispec.Descriptor
	img  ocispec.Image
}

// Import implements Importer.
func (importer *Importer) Import(ctx context.Context, store content.Store, reader io.Reader) ([]images.Image, error) {
	tr := tar.NewReader(reader)
	var (
		mfsts   []manifest
		layers  = make(map[string]ocispec.Descriptor, 0) // key: filename (deadbeeddeadbeef/layer.tar)
		configs = make(map[string]imageConfig, 0)        // key: filename (deadbeeddeadbeef.json)
	)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		hdrName := path.Clean(hdr.Name)
		if hdrName == "manifest.json" {
			mfsts, err = onUntarManifestJSON(tr)
			if err != nil {
				return nil, err
			}
			continue
		}
		if isLayerTar(hdr.Name) {
			desc, err := onUntarLayerTar(ctx, tr, store, hdrName, hdr.Size)
			if err != nil {
				return nil, err
			}
			layers[hdrName] = *desc
			continue
		}
		if isDotJSON(hdr.Name) {
			c, err := onUntarDotJSON(ctx, tr, store, hdrName, hdr.Size)
			if err != nil {
				return nil, err
			}
			configs[hdrName] = *c
			continue
		}
	}
	if len(mfsts) == 0 {
		return nil, errors.New("no manifest found")
	}
	var imgrecs []images.Image
	for _, mfst := range mfsts {
		if mfst.Parent != "" {
			return nil, errors.Errorf("unsupported mfst.Parent=%q", mfst.Parent)
		}
		config, ok := configs[mfst.Config]
		if !ok {
			return nil, errors.Errorf("image config %q not found", mfst.Config)
		}
		schema2Manifest, err := makeDockerSchema2Manifest(mfst, config, layers)
		if err != nil {
			return nil, err
		}
		desc, err := writeDockerSchema2Manifest(ctx, store, schema2Manifest, config.img.Architecture, config.img.OS)
		if err != nil {
			return nil, err
		}

		for _, ref := range mfst.RepoTags {
			normalized, err := normalizeImageRef(ref, importer.Conservative)
			if err != nil {
				return imgrecs, errors.Wrapf(err, "normalize image ref %q", ref)
			}
			ref = normalized.String()
			imgrecs = append(imgrecs, images.Image{
				Name:   ref,
				Target: *desc,
			})
		}
	}
	return imgrecs, nil
}

func makeDockerSchema2Manifest(mfst manifest, config imageConfig, layers map[string]ocispec.Descriptor) (*ocispec.Manifest, error) {
	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		Config: config.desc,
	}
	for _, f := range mfst.Layers {
		desc, ok := layers[f]
		if !ok {
			return nil, errors.Errorf("layer %q not found", f)
		}
		manifest.Layers = append(manifest.Layers, desc)
	}
	return &manifest, nil
}

func writeDockerSchema2Manifest(ctx context.Context, store content.Store, manifest *ocispec.Manifest, arch, os string) (*ocispec.Descriptor, error) {
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	manifestBytesR := bytes.NewReader(manifestBytes)
	manifestDigest := digest.FromBytes(manifestBytes)
	labels := map[string]string{}
	labels["containerd.io/gc.ref.content.0"] = manifest.Config.Digest.String()
	for i, ch := range manifest.Layers {
		labels[fmt.Sprintf("containerd.io/gc.ref.content.%d", i+1)] = ch.Digest.String()
	}
	if err := content.WriteBlob(ctx, store, "manifest-"+manifestDigest.String(), manifestBytesR,
		int64(len(manifestBytes)), manifestDigest, content.WithLabels(labels)); err != nil {
		return nil, err
	}

	desc := &ocispec.Descriptor{
		MediaType: images.MediaTypeDockerSchema2Manifest,
		Digest:    manifestDigest,
		Size:      int64(len(manifestBytes)),
	}
	if arch != "" || os != "" {
		desc.Platform = &ocispec.Platform{
			Architecture: arch,
			OS:           os,
		}
	}
	return desc, nil
}

func onUntarManifestJSON(r io.Reader) ([]manifest, error) {
	// name: "manifest.json"
	b, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var mfsts []manifest
	if err := json.Unmarshal(b, &mfsts); err != nil {
		return nil, err
	}
	return mfsts, nil
}

func onUntarLayerTar(ctx context.Context, r io.Reader, store content.Store, name string, size int64) (*ocispec.Descriptor, error) {
	// name is like "deadbeeddeadbeef/layer.tar" ( guaranteed by isLayerTar() )
	split := strings.Split(name, "/")
	// note: split[0] is not expected digest here
	cw, err := store.Writer(ctx, "layer-"+split[0], size, "")
	if err != nil {
		return nil, err
	}
	defer cw.Close()
	_, err = io.Copy(cw, r)
	if err != nil {
		return nil, err
	}
	if err = cw.Commit(ctx, size, ""); err != nil {
		return nil, err
	}
	desc := ocispec.Descriptor{
		MediaType: images.MediaTypeDockerSchema2Layer,
		Size:      size,
	}
	desc.Digest = cw.Digest()
	return &desc, nil
}

func onUntarDotJSON(ctx context.Context, r io.Reader, store content.Store, name string, size int64) (*imageConfig, error) {
	config := imageConfig{}
	config.desc.MediaType = images.MediaTypeDockerSchema2Config
	config.desc.Size = size
	// name is like "deadbeeddeadbeef.json" ( guaranteed by is DotJSON() )
	cw, err := store.Writer(ctx, "config-"+name, size, "")
	if err != nil {
		return nil, err
	}
	defer cw.Close()
	var buf bytes.Buffer
	tr := io.TeeReader(r, &buf)
	_, err = io.Copy(cw, tr)
	if err != nil {
		return nil, err
	}
	if err = cw.Commit(ctx, size, ""); err != nil {
		return nil, err
	}
	config.desc.Digest = cw.Digest()
	if err := json.Unmarshal(buf.Bytes(), &config.img); err != nil {
		return nil, err
	}
	return &config, nil
}
