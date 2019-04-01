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

package archive

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/platforms"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type exportOptions struct {
	manifests          []ocispec.Descriptor
	platform           platforms.MatchComparer
	allPlatforms       bool
	skipDockerManifest bool
}

// ExportOpt defines options for configuring exported descriptors
type ExportOpt func(context.Context, *exportOptions) error

// WithPlatform defines the platform to require manifest lists have
// not exporting all platforms.
// Additionally, platform is used to resolve image configs for
// Docker v1.1, v1.2 format compatibility.
func WithPlatform(p platforms.MatchComparer) ExportOpt {
	return func(ctx context.Context, o *exportOptions) error {
		o.platform = p
		return nil
	}
}

// WithAllPlatforms exports all manifests from a manifest list.
// Missing content will fail the export.
func WithAllPlatforms() ExportOpt {
	return func(ctx context.Context, o *exportOptions) error {
		o.allPlatforms = true
		return nil
	}
}

// WithSkipDockerManifest skips creation of the Docker compatible
// manifest.json file.
func WithSkipDockerManifest() ExportOpt {
	return func(ctx context.Context, o *exportOptions) error {
		o.skipDockerManifest = true
		return nil
	}
}

// WithImage adds the provided images to the exported archive.
func WithImage(is images.Store, name string) ExportOpt {
	return func(ctx context.Context, o *exportOptions) error {
		img, err := is.Get(ctx, name)
		if err != nil {
			return err
		}

		var i int
		o.manifests, i = appendDescriptor(o.manifests, img.Target)
		o.manifests[i].Annotations = addNameAnnotation(name, o.manifests[i].Annotations)

		return nil
	}
}

// WithManifest adds a manifest to the exported archive.
// It is up to caller to put name annotation to on the manifest
// descriptor if needed.
func WithManifest(manifest ocispec.Descriptor) ExportOpt {
	return func(ctx context.Context, o *exportOptions) error {
		var i int
		o.manifests, i = appendDescriptor(o.manifests, manifest)
		o.manifests[i].Annotations = manifest.Annotations
		return nil
	}
}

// WithNamedManifest adds a manifest to the exported archive
// with the provided names.
func WithNamedManifest(manifest ocispec.Descriptor, names ...string) ExportOpt {
	return func(ctx context.Context, o *exportOptions) error {
		var i int
		o.manifests, i = appendDescriptor(o.manifests, manifest)
		for _, name := range names {
			o.manifests[i].Annotations = addNameAnnotation(name, o.manifests[i].Annotations)
		}

		return nil
	}
}

func appendDescriptor(descs []ocispec.Descriptor, desc ocispec.Descriptor) ([]ocispec.Descriptor, int) {
	i := 0
	for i < len(descs) {
		if descs[i].Digest == desc.Digest {
			return descs, i
		}
		i++
	}
	return append(descs, desc), i
}

func addNameAnnotation(name string, annotations map[string]string) map[string]string {
	if annotations == nil {
		annotations = map[string]string{}
	}

	i := 0
	for {
		key := images.AnnotationImageName
		if i > 0 {
			key = fmt.Sprintf("%sextra.%d", images.AnnotationImageNamePrefix, i)
		}
		i++

		if val, ok := annotations[key]; ok {
			if val != name {
				continue
			}
		} else {
			annotations[key] = name
		}
		break
	}
	return annotations
}

// Export implements Exporter.
func Export(ctx context.Context, store content.Provider, writer io.Writer, opts ...ExportOpt) error {
	var eo exportOptions
	for _, opt := range opts {
		if err := opt(ctx, &eo); err != nil {
			return err
		}
	}

	records := []tarRecord{
		ociLayoutFile(""),
		ociIndexRecord(eo.manifests),
	}

	algorithms := map[string]struct{}{}
	manifestTags := map[string]ocispec.Descriptor{}
	for _, desc := range eo.manifests {
		switch desc.MediaType {
		case images.MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest:
			r, err := getRecords(ctx, store, desc, algorithms)
			if err != nil {
				return err
			}
			records = append(records, r...)

			for _, name := range imageNames(desc.Annotations) {
				manifestTags[name] = desc
			}
		case images.MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex:
			records = append(records, blobRecord(store, desc))

			p, err := content.ReadBlob(ctx, store, desc)
			if err != nil {
				return err
			}

			var index ocispec.Index
			if err := json.Unmarshal(p, &index); err != nil {
				return err
			}

			names := imageNames(desc.Annotations)
			var manifests []ocispec.Descriptor
			for _, m := range index.Manifests {
				if eo.platform != nil {
					if m.Platform == nil || eo.platform.Match(*m.Platform) {
						manifests = append(manifests, m)
					} else if !eo.allPlatforms {
						continue
					}
				}

				r, err := getRecords(ctx, store, m, algorithms)
				if err != nil {
					return err
				}

				records = append(records, r...)
			}

			if len(names) > 0 && !eo.skipDockerManifest {
				if len(manifests) >= 1 {
					if len(manifests) > 1 {
						sort.SliceStable(manifests, func(i, j int) bool {
							if manifests[i].Platform == nil {
								return false
							}
							if manifests[j].Platform == nil {
								return true
							}
							return eo.platform.Less(*manifests[i].Platform, *manifests[j].Platform)
						})
					}
					for _, name := range names {
						manifestTags[name] = manifests[0]
					}
				} else if eo.platform != nil {
					return errors.Wrap(errdefs.ErrNotFound, "no manifest found for platform")
				}
			}
		default:
			return errors.Wrap(errdefs.ErrInvalidArgument, "only manifests may be exported")
		}
	}

	if len(manifestTags) > 0 {
		tr, err := manifestsRecord(ctx, store, manifestTags)
		if err != nil {
			return errors.Wrap(err, "unable to create manifests file")
		}

		records = append(records, tr)
	}

	if len(algorithms) > 0 {
		records = append(records, directoryRecord("blobs/", 0755))
		for alg := range algorithms {
			records = append(records, directoryRecord("blobs/"+alg+"/", 0755))
		}
	}

	tw := tar.NewWriter(writer)
	defer tw.Close()
	return writeTar(ctx, tw, records)
}

func imageNames(annotations map[string]string) []string {
	var names []string
	for k, v := range annotations {
		if k == images.AnnotationImageName || strings.HasPrefix(k, images.AnnotationImageName) {
			names = append(names, v)
		}
	}
	return names
}

func getRecords(ctx context.Context, store content.Provider, desc ocispec.Descriptor, algorithms map[string]struct{}) ([]tarRecord, error) {
	var records []tarRecord
	exportHandler := func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		records = append(records, blobRecord(store, desc))
		algorithms[desc.Digest.Algorithm().String()] = struct{}{}
		return nil, nil
	}

	childrenHandler := images.ChildrenHandler(store)

	handlers := images.Handlers(
		childrenHandler,
		images.HandlerFunc(exportHandler),
	)

	// Walk sequentially since the number of fetchs is likely one and doing in
	// parallel requires locking the export handler
	if err := images.Walk(ctx, handlers, desc); err != nil {
		return nil, err
	}

	return records, nil
}

type tarRecord struct {
	Header *tar.Header
	CopyTo func(context.Context, io.Writer) (int64, error)
}

func blobRecord(cs content.Provider, desc ocispec.Descriptor) tarRecord {
	path := path.Join("blobs", desc.Digest.Algorithm().String(), desc.Digest.Encoded())
	return tarRecord{
		Header: &tar.Header{
			Name:     path,
			Mode:     0444,
			Size:     desc.Size,
			Typeflag: tar.TypeReg,
		},
		CopyTo: func(ctx context.Context, w io.Writer) (int64, error) {
			r, err := cs.ReaderAt(ctx, desc)
			if err != nil {
				return 0, errors.Wrap(err, "failed to get reader")
			}
			defer r.Close()

			// Verify digest
			dgstr := desc.Digest.Algorithm().Digester()

			n, err := io.Copy(io.MultiWriter(w, dgstr.Hash()), content.NewReader(r))
			if err != nil {
				return 0, errors.Wrap(err, "failed to copy to tar")
			}
			if dgstr.Digest() != desc.Digest {
				return 0, errors.Errorf("unexpected digest %s copied", dgstr.Digest())
			}
			return n, nil
		},
	}
}

func directoryRecord(name string, mode int64) tarRecord {
	return tarRecord{
		Header: &tar.Header{
			Name:     name,
			Mode:     mode,
			Typeflag: tar.TypeDir,
		},
	}
}

func ociLayoutFile(version string) tarRecord {
	if version == "" {
		version = ocispec.ImageLayoutVersion
	}
	layout := ocispec.ImageLayout{
		Version: version,
	}

	b, err := json.Marshal(layout)
	if err != nil {
		panic(err)
	}

	return tarRecord{
		Header: &tar.Header{
			Name:     ocispec.ImageLayoutFile,
			Mode:     0444,
			Size:     int64(len(b)),
			Typeflag: tar.TypeReg,
		},
		CopyTo: func(ctx context.Context, w io.Writer) (int64, error) {
			n, err := w.Write(b)
			return int64(n), err
		},
	}

}

func ociIndexRecord(manifests []ocispec.Descriptor) tarRecord {
	index := ocispec.Index{
		Versioned: ocispecs.Versioned{
			SchemaVersion: 2,
		},
		Manifests: manifests,
	}

	b, err := json.Marshal(index)
	if err != nil {
		panic(err)
	}

	return tarRecord{
		Header: &tar.Header{
			Name:     "index.json",
			Mode:     0644,
			Size:     int64(len(b)),
			Typeflag: tar.TypeReg,
		},
		CopyTo: func(ctx context.Context, w io.Writer) (int64, error) {
			n, err := w.Write(b)
			return int64(n), err
		},
	}
}

func manifestsRecord(ctx context.Context, store content.Provider, manifests map[string]ocispec.Descriptor) (tarRecord, error) {
	type mfst struct {
		Config   string
		RepoTags []string
		Layers   []string
	}

	images := map[digest.Digest]mfst{}
	for name, m := range manifests {
		p, err := content.ReadBlob(ctx, store, m)
		if err != nil {
			return tarRecord{}, err
		}

		var manifest ocispec.Manifest
		if err := json.Unmarshal(p, &manifest); err != nil {
			return tarRecord{}, err
		}
		if err := manifest.Config.Digest.Validate(); err != nil {
			return tarRecord{}, errors.Wrapf(err, "invalid manifest %q", m.Digest)
		}

		nname, err := familiarizeReference(name)
		if err != nil {
			return tarRecord{}, err
		}

		dgst := manifest.Config.Digest
		mf, ok := images[dgst]
		if !ok {
			mf.Config = path.Join("blobs", dgst.Algorithm().String(), dgst.Encoded())
			for _, l := range manifest.Layers {
				path := path.Join("blobs", l.Digest.Algorithm().String(), l.Digest.Encoded())
				mf.Layers = append(mf.Layers, path)
			}
		}

		mf.RepoTags = append(mf.RepoTags, nname)

		images[dgst] = mf
	}

	var mfsts []mfst
	for _, mf := range images {
		mfsts = append(mfsts, mf)
	}

	b, err := json.Marshal(mfsts)
	if err != nil {
		return tarRecord{}, err
	}

	return tarRecord{
		Header: &tar.Header{
			Name:     "manifest.json",
			Mode:     0644,
			Size:     int64(len(b)),
			Typeflag: tar.TypeReg,
		},
		CopyTo: func(ctx context.Context, w io.Writer) (int64, error) {
			n, err := w.Write(b)
			return int64(n), err
		},
	}, nil
}

func writeTar(ctx context.Context, tw *tar.Writer, records []tarRecord) error {
	sort.Slice(records, func(i, j int) bool {
		return records[i].Header.Name < records[j].Header.Name
	})

	var last string
	for _, record := range records {
		if record.Header.Name == last {
			continue
		}
		last = record.Header.Name
		if err := tw.WriteHeader(record.Header); err != nil {
			return err
		}
		if record.CopyTo != nil {
			n, err := record.CopyTo(ctx, tw)
			if err != nil {
				return err
			}
			if n != record.Header.Size {
				return errors.Errorf("unexpected copy size for %s", record.Header.Name)
			}
		} else if record.Header.Size > 0 {
			return errors.Errorf("no content to write to record with non-zero size for %s", record.Header.Name)
		}
	}
	return nil
}
