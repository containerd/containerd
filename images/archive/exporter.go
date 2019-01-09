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
	"io"
	"path"
	"sort"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	ocispecs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// Export exports an image tarball into writer.
// The out tarball conforms to OCI Image Format Specification.
//
// It is up to caller to put "org.opencontainers.image.ref.name" annotation to desc.
//
// When dockerName is non-empty, the output tarball becomes also compatible with
// Docker Combined Image JSON + Filesystem Changeset Format v1.1
// https://github.com/moby/moby/blob/master/image/spec/v1.1.md#combined-image-json--filesystem-changeset-format
func Export(ctx context.Context, store content.Provider, desc ocispec.Descriptor, dockerName string, writer io.Writer) error {
	tw := tar.NewWriter(writer)
	defer tw.Close()

	records := []tarRecord{
		ociLayoutFile(""),
		ociIndexRecord(desc),
	}

	if dockerName != "" {
		dockerManifest, err := dockerManifestRecord(ctx, store, desc, dockerName)
		if err != nil {
			return err
		}
		records = append(records, *dockerManifest)
	}

	algorithms := map[string]struct{}{}
	exportHandler := func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		records = append(records, blobRecord(store, desc))
		algorithms[desc.Digest.Algorithm().String()] = struct{}{}
		return nil, nil
	}

	handlers := images.Handlers(
		images.ChildrenHandler(store),
		images.HandlerFunc(exportHandler),
	)

	// Walk sequentially since the number of fetchs is likely one and doing in
	// parallel requires locking the export handler
	if err := images.Walk(ctx, handlers, desc); err != nil {
		return err
	}

	if len(algorithms) > 0 {
		records = append(records, directoryRecord("blobs/", 0755))
		for alg := range algorithms {
			records = append(records, directoryRecord("blobs/"+alg+"/", 0755))
		}
	}

	return writeTar(ctx, tw, records)
}

type tarRecord struct {
	Header *tar.Header
	CopyTo func(context.Context, io.Writer) (int64, error)
}

func dockerManifestRecord(ctx context.Context, provider content.Provider, desc ocispec.Descriptor, name string) (*tarRecord, error) {
	switch desc.MediaType {
	case images.MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest:
		p, err := content.ReadBlob(ctx, provider, desc)
		if err != nil {
			return nil, err
		}
		var manifest ocispec.Manifest
		if err := json.Unmarshal(p, &manifest); err != nil {
			return nil, err
		}
		type mfstItem struct {
			Config   string
			RepoTags []string
			Layers   []string
		}
		item := mfstItem{
			Config: path.Join("blobs", manifest.Config.Digest.Algorithm().String(), manifest.Config.Digest.Hex()),
		}

		for _, l := range manifest.Layers {
			item.Layers = append(item.Layers, path.Join("blobs", l.Digest.Algorithm().String(), l.Digest.Hex()))
		}

		if name != "" {
			item.RepoTags = append(item.RepoTags, name)
		}

		dt, err := json.Marshal([]mfstItem{item})
		if err != nil {
			return nil, err
		}

		return &tarRecord{
			Header: &tar.Header{
				Name:     "manifest.json",
				Mode:     0444,
				Size:     int64(len(dt)),
				Typeflag: tar.TypeReg,
			},
			CopyTo: func(ctx context.Context, w io.Writer) (int64, error) {
				n, err := w.Write(dt)
				return int64(n), err
			},
		}, nil
	default:
		return nil, errors.Errorf("%v not supported for Docker exporter", desc.MediaType)
	}

}

func blobRecord(cs content.Provider, desc ocispec.Descriptor) tarRecord {
	path := "blobs/" + desc.Digest.Algorithm().String() + "/" + desc.Digest.Hex()
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

func ociIndexRecord(manifests ...ocispec.Descriptor) tarRecord {
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

func writeTar(ctx context.Context, tw *tar.Writer, records []tarRecord) error {
	sort.Slice(records, func(i, j int) bool {
		return records[i].Header.Name < records[j].Header.Name
	})

	for _, record := range records {
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
