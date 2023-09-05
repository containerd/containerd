//go:build gofuzz

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

package fuzz

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path"

	fuzz "github.com/AdaLogics/go-fuzz-headers"

	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/content/local"
	imageArchive "github.com/containerd/containerd/images/archive"
	"github.com/containerd/containerd/log"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// FuzzApply implements a fuzzer that applies
// a fuzzed tar archive on a directory
func FuzzApply(data []byte) int {
	ctx := context.Background()

	// Apply() is logging the below message, which is too noisy and not really useful
	// if the input is random.
	//
	// level=warning msg="ignored xattr ... in archive" error="operation not supported"
	log.G(ctx).Logger.SetLevel(log.PanicLevel)

	f := fuzz.NewConsumer(data)
	iters, err := f.GetInt()
	if err != nil {
		return 0
	}
	maxIters := 20
	tmpDir, err := os.MkdirTemp("", "prefix-test")
	if err != nil {
		return 0
	}
	defer os.RemoveAll(tmpDir)
	for i := 0; i < iters%maxIters; i++ {
		rBytes, err := f.TarBytes()
		if err != nil {
			return 0
		}
		r := bytes.NewReader(rBytes)
		_, _ = archive.Apply(ctx, tmpDir, r)
	}
	return 1
}

// FuzzImportIndex implements a fuzzer
// that targets archive.ImportIndex()
func FuzzImportIndex(data []byte) int {
	f := fuzz.NewConsumer(data)
	tarBytes, err := f.TarBytes()
	if err != nil {
		return 0
	}
	var r *bytes.Reader
	ctx := context.Background()
	r = bytes.NewReader(tarBytes)
	shouldRequireLayoutOrManifest, err := f.GetBool()
	if err != nil {
		return 0
	}
	if shouldRequireLayoutOrManifest {
		hasLayoutOrManifest := false
		tr := tar.NewReader(r)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return 0
			}
			hdrName := path.Clean(hdr.Name)
			switch hdrName {
			case ocispec.ImageLayoutFile, "manifest.json":
				hasLayoutOrManifest = true
			}
		}
		if !hasLayoutOrManifest {
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			defer tw.Close()
			tr := tar.NewReader(r)
			for {
				hdr, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return 0
				}
				fileContents, err := io.ReadAll(tr)
				if err != nil {
					return 0
				}
				tw.WriteHeader(hdr)
				tw.Write(fileContents)
			}
			manifestFileContents, err := f.GetBytes()
			if err != nil {
				return 0
			}
			tw.WriteHeader(&tar.Header{
				Name:     "manifest.json",
				Mode:     0644,
				Size:     int64(len(manifestFileContents)),
				Typeflag: tar.TypeReg,
			})
			tw.Write(manifestFileContents)
			r = bytes.NewReader(buf.Bytes())
		}
	}
	tmpdir, err := os.MkdirTemp("", "fuzzing-")
	if err != nil {
		return 0
	}
	cs, err := local.NewStore(tmpdir)
	if err != nil {
		return 0
	}
	_, _ = imageArchive.ImportIndex(ctx, cs, r)
	return 1
}
