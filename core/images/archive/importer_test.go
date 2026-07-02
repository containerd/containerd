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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type fileEntry struct {
	name     string
	data     []byte
	linkname string
}

func TestImportIndex(t *testing.T) {
	cases := []struct {
		name  string
		files []fileEntry
	}{
		{
			name: "StandardArchive",
			files: []fileEntry{
				{name: "config.json", data: []byte("{}")},
				{name: "layer.tar", data: []byte("standard layer data")},
				{name: "link.tar", linkname: "layer.tar"},
			},
		},
		{
			name: "RelativeSymlinkInSubdir",
			files: []fileEntry{
				{name: "config.json", data: []byte("{}")},
				{name: "blob.tar", data: []byte("regression layer data")},
				{name: "subdir/layer.tar", linkname: "blob.tar"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := &mockStore{blobs: make(map[digest.Digest][]byte)}

			buf := buildTestTar(t, tc.files)

			_, err := ImportIndex(ctx, store, buf)
			if err != nil {
				t.Fatalf("Import failed: %v", err)
			}
		})
	}
}

func buildTestTar(t *testing.T, files []fileEntry) io.Reader {
	buf := new(bytes.Buffer)
	tw := tar.NewWriter(buf)

	var layerPaths []string
	var configPath string

	for _, f := range files {
		hdr := &tar.Header{Name: f.name, Mode: 0644}

		if f.linkname != "" {
			hdr.Typeflag = tar.TypeSymlink
			hdr.Linkname = f.linkname
		} else {
			hdr.Typeflag = tar.TypeReg
			hdr.Size = int64(len(f.data))
		}

		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if f.linkname == "" {
			tw.Write(f.data)
		}

		if f.name == "config.json" {
			configPath = f.name
		} else {
			layerPaths = append(layerPaths, f.name)
		}
	}

	manifest := []struct {
		Config string   `json:"Config"`
		Layers []string `json:"Layers"`
	}{
		{Config: configPath, Layers: layerPaths},
	}
	mdata, _ := json.Marshal(manifest)
	tw.WriteHeader(&tar.Header{Name: "manifest.json", Size: int64(len(mdata)), Typeflag: tar.TypeReg})
	tw.Write(mdata)

	tw.Close()
	return buf
}

type mockStore struct {
	content.Store
	blobs map[digest.Digest][]byte
}

func (m *mockStore) Writer(ctx context.Context, opts ...content.WriterOpt) (content.Writer, error) {
	return &mockWriter{store: m}, nil
}
func (m *mockStore) ReaderAt(ctx context.Context, desc ocispec.Descriptor) (content.ReaderAt, error) {
	data, ok := m.blobs[desc.Digest]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &mockReaderAt{reader: bytes.NewReader(data)}, nil
}
func (m *mockStore) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	return content.Info{Digest: dgst, Size: int64(len(m.blobs[dgst]))}, nil
}
func (m *mockStore) Walk(ctx context.Context, fn content.WalkFunc, filters ...string) error {
	return nil
}
func (m *mockStore) Status(ctx context.Context, ref string) (content.Status, error) {
	return content.Status{}, nil
}
func (m *mockStore) Abort(ctx context.Context, ref string) error { return nil }

type mockWriter struct {
	store  *mockStore
	buffer bytes.Buffer
}

func (w *mockWriter) Write(p []byte) (int, error) { return w.buffer.Write(p) }
func (w *mockWriter) Close() error                { return nil }
func (w *mockWriter) Digest() digest.Digest       { return digest.FromBytes(w.buffer.Bytes()) }
func (w *mockWriter) Commit(ctx context.Context, size int64, expected digest.Digest, opts ...content.Opt) error {
	w.store.blobs[w.Digest()] = w.buffer.Bytes()
	return nil
}
func (w *mockWriter) Status() (content.Status, error) { return content.Status{}, nil }
func (w *mockWriter) Truncate(size int64) error       { return nil }

type mockReaderAt struct{ reader *bytes.Reader }

func (r *mockReaderAt) ReadAt(p []byte, off int64) (int, error) { return r.reader.ReadAt(p, off) }
func (r *mockReaderAt) Size() int64                             { return r.reader.Size() }
func (r *mockReaderAt) Close() error                            { return nil }
