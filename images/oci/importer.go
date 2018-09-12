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

// Package oci provides the importer and the exporter for OCI Image Spec.
package oci

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"path"
	"strings"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// V1Importer implements OCI Image Spec v1.
type V1Importer struct{}

var _ images.Importer = &V1Importer{}

// Import implements Importer.
func (oi *V1Importer) Import(ctx context.Context, store content.Store, reader io.Reader) (ocispec.Descriptor, error) {
	var (
		desc ocispec.Descriptor
		tr   = tar.NewReader(reader)
	)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ocispec.Descriptor{}, err
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
		} else {
			log.G(ctx).WithField("file", hdr.Name).Debug("unknown file ignored")
		}
	}
	if desc.Digest == "" {
		return ocispec.Descriptor{}, errors.New("no index.json found")
	}

	return desc, nil
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

// DigestTranslator creates a digest reference by adding the
// digest to an image name
func DigestTranslator(prefix string) func(digest.Digest) string {
	return func(dgst digest.Digest) string {
		return prefix + "@" + dgst.String()
	}
}
