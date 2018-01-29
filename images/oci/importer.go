// Package oci provides the importer and the exporter for OCI Image Spec.
package oci

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"path"
	"strings"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// V1Importer implements OCI Image Spec v1.
type V1Importer struct {
	// ImageName is preprended to either `:` + OCI ref name or `@` + digest (for anonymous refs).
	// This field is mandatory atm, but may change in the future. maybe ref map[string]string as in moby/moby#33355
	ImageName string
}

var _ images.Importer = &V1Importer{}

// Import implements Importer.
func (oi *V1Importer) Import(ctx context.Context, store content.Store, reader io.Reader) ([]images.Image, error) {
	if oi.ImageName == "" {
		return nil, errors.New("ImageName not set")
	}
	tr := tar.NewReader(reader)
	var imgrecs []images.Image
	foundIndexJSON := false
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
		if hdrName == "index.json" {
			if foundIndexJSON {
				return nil, errors.New("duplicated index.json")
			}
			foundIndexJSON = true
			imgrecs, err = onUntarIndexJSON(tr, oi.ImageName)
			if err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(hdrName, "blobs/") {
			if err := onUntarBlob(ctx, tr, store, hdrName, hdr.Size); err != nil {
				return nil, err
			}
		}
	}
	if !foundIndexJSON {
		return nil, errors.New("no index.json found")
	}
	for _, img := range imgrecs {
		err := setGCRefContentLabels(ctx, store, img.Target)
		if err != nil {
			return imgrecs, err
		}
	}
	// FIXME(AkihiroSuda): set GC labels for unreferrenced blobs (i.e. with unknown media types)?
	return imgrecs, nil
}

func onUntarIndexJSON(r io.Reader, imageName string) ([]images.Image, error) {
	b, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var idx ocispec.Index
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, err
	}
	var imgrecs []images.Image
	for _, m := range idx.Manifests {
		ref, err := normalizeImageRef(imageName, m)
		if err != nil {
			return nil, err
		}
		imgrecs = append(imgrecs, images.Image{
			Name:   ref,
			Target: m,
		})
	}
	return imgrecs, nil
}

func normalizeImageRef(imageName string, manifest ocispec.Descriptor) (string, error) {
	digest := manifest.Digest
	if digest == "" {
		return "", errors.Errorf("manifest with empty digest: %v", manifest)
	}
	ociRef := manifest.Annotations[ocispec.AnnotationRefName]
	if ociRef == "" {
		return imageName + "@" + digest.String(), nil
	}
	return imageName + ":" + ociRef, nil
}

func onUntarBlob(ctx context.Context, r io.Reader, store content.Store, name string, size int64) error {
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
	return content.WriteBlob(ctx, store, "unknown-"+dgst.String(), r, size, dgst)
}

// GetChildrenDescriptors returns children blob descriptors for the following supported types:
// - images.MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest
// - images.MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex
func GetChildrenDescriptors(r io.Reader, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	switch desc.MediaType {
	case images.MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest:
		var manifest ocispec.Manifest
		if err := json.NewDecoder(r).Decode(&manifest); err != nil {
			return nil, err
		}
		return append([]ocispec.Descriptor{manifest.Config}, manifest.Layers...), nil
	case images.MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex:
		var index ocispec.Index
		if err := json.NewDecoder(r).Decode(&index); err != nil {
			return nil, err
		}
		return index.Manifests, nil
	}
	return nil, nil
}

func setGCRefContentLabels(ctx context.Context, store content.Store, desc ocispec.Descriptor) error {
	info, err := store.Info(ctx, desc.Digest)
	if err != nil {
		if errdefs.IsNotFound(err) {
			// when the archive is created from multi-arch image,
			// it may contain only blobs for a certain platform.
			// So ErrNotFound (on manifest list) is expected here.
			return nil
		}
		return err
	}
	ra, err := store.ReaderAt(ctx, desc.Digest)
	if err != nil {
		return err
	}
	defer ra.Close()
	r := content.NewReader(ra)
	children, err := GetChildrenDescriptors(r, desc)
	if err != nil {
		return err
	}
	if info.Labels == nil {
		info.Labels = map[string]string{}
	}
	for i, child := range children {
		// Note: child blob is not guaranteed to be written to the content store. (multi-arch)
		info.Labels[fmt.Sprintf("containerd.io/gc.ref.content.%d", i)] = child.Digest.String()
	}
	if _, err := store.Update(ctx, info, "labels"); err != nil {
		return err
	}
	for _, child := range children {
		if err := setGCRefContentLabels(ctx, store, child); err != nil {
			return err
		}
	}
	return nil
}
