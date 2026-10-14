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

// pkg2oci assembles pre-built package blobs (tar+zstd archives) or a Flox
// environment into an OCI image layout tarball importable with "ctr images import".
//
// Usage (pre-built blobs):
//
//	pkg2oci --output image.tar \
//	  --package /path/to/glibc.tar.zst \
//	  --package /path/to/myapp.tar.zst \
//	  localhost/myteam/myapp:1.0
//
// Usage (Flox environment):
//
//	pkg2oci --output image.tar \
//	  --flox-env ./myproject \
//	  --entrypoint myapp \
//	  localhost/myteam/myapp:1.0
//
//	pkg2oci --output image.tar \
//	  --flox-env owner/myenv \
//	  --entrypoint myapp \
//	  localhost/myteam/myapp:1.0
package main

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/urfave/cli/v2"

	"github.com/klauspost/compress/zstd"
)

func main() {
	app := &cli.App{
		Name:      "pkg2oci",
		Usage:     "assemble package blobs or a Flox environment into an OCI image tarball",
		ArgsUsage: "<image>",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "output",
				Aliases:  []string{"o"},
				Usage:    "output OCI archive tar `path`",
				Required: true,
			},
			&cli.StringSliceFlag{
				Name:  "package",
				Usage: "package blob path (tar+zstd); repeatable, base-first",
			},
			&cli.StringFlag{
				Name:  "flox-env",
				Usage: "Flox environment: local path or FloxHub owner/name",
			},
			&cli.StringSliceFlag{
				Name:  "entrypoint",
				Usage: "container entrypoint token; repeatable",
			},
			&cli.StringSliceFlag{
				Name:  "cmd",
				Usage: "container default command token; repeatable",
			},
			&cli.StringSliceFlag{
				Name:  "env",
				Usage: "environment variable KEY=VALUE; repeatable",
			},
		},
		Action: func(c *cli.Context) error {
			if c.NArg() != 1 {
				return fmt.Errorf("expected <image>; got %d arguments", c.NArg())
			}
			if len(c.StringSlice("package")) == 0 && c.String("flox-env") == "" {
				return fmt.Errorf("one of --package or --flox-env is required")
			}
			return run(
				c.Args().Get(0),
				c.String("flox-env"),
				c.StringSlice("package"),
				c.String("output"),
				c.StringSlice("entrypoint"),
				c.StringSlice("cmd"),
				c.StringSlice("env"),
			)
		},
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "pkg2oci:", err)
		os.Exit(1)
	}
}

type layerInfo struct {
	desc   ocispec.Descriptor
	diffID digest.Digest
	path   string
}

func run(imageRef, floxEnvArg string, packagePaths []string, output string, entrypoint, cmd, envVars []string) error {
	imageRef = ensureTag(imageRef)

	var layers []layerInfo

	if floxEnvArg != "" {
		tmpDir, err := os.MkdirTemp("", "pkg2oci-flox-*")
		if err != nil {
			return fmt.Errorf("creating temp dir: %w", err)
		}
		defer os.RemoveAll(tmpDir)

		blobPaths, baseEntrypoint, err := buildFloxLayers(floxEnvArg, tmpDir)
		if err != nil {
			return fmt.Errorf("building flox layers: %w", err)
		}
		entrypoint = append(baseEntrypoint, entrypoint...)

		for _, path := range blobPaths {
			info, err := hashBlob(path)
			if err != nil {
				return fmt.Errorf("hashing flox blob %q: %w", path, err)
			}
			layers = append(layers, info)
		}
	}

	for _, path := range packagePaths {
		info, err := hashBlob(path)
		if err != nil {
			return fmt.Errorf("hashing %q: %w", path, err)
		}
		layers = append(layers, info)
	}

	diffIDs := make([]digest.Digest, len(layers))
	descs := make([]ocispec.Descriptor, len(layers))
	for i, l := range layers {
		diffIDs[i] = l.diffID
		descs[i] = l.desc
	}

	arch := runtime.GOARCH
	configJSON, err := json.Marshal(ocispec.Image{
		Platform: ocispec.Platform{Architecture: arch, OS: "linux"},
		Config: ocispec.ImageConfig{
			Env:        envVars,
			Entrypoint: entrypoint,
			Cmd:        cmd,
		},
		RootFS: ocispec.RootFS{Type: "layers", DiffIDs: diffIDs},
	})
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	configDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.FromBytes(configJSON),
		Size:      int64(len(configJSON)),
	}

	manifestJSON, err := json.MarshalIndent(ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    configDesc,
		Layers:    descs,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	manifestDesc := ocispec.Descriptor{
		MediaType:   ocispec.MediaTypeImageManifest,
		Digest:      digest.FromBytes(manifestJSON),
		Size:        int64(len(manifestJSON)),
		Platform:    &ocispec.Platform{OS: "linux", Architecture: arch},
		Annotations: map[string]string{ocispec.AnnotationRefName: imageRef},
	}

	indexJSON, err := json.MarshalIndent(ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{manifestDesc},
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal index: %w", err)
	}

	return writeOCITar(output, layers, configDesc, configJSON, manifestDesc, manifestJSON, indexJSON)
}

func writeOCITar(output string, layers []layerInfo, configDesc ocispec.Descriptor, configJSON []byte, manifestDesc ocispec.Descriptor, manifestJSON []byte, indexJSON []byte) error {
	f, err := os.Create(output)
	if err != nil {
		return err
	}
	defer f.Close()

	tw := tar.NewWriter(f)
	defer tw.Close()

	addBytes := func(name string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(data)), Mode: 0444}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}

	if err := addBytes("oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`)); err != nil {
		return err
	}

	for _, l := range layers {
		lf, err := os.Open(l.path)
		if err != nil {
			return err
		}
		err = func() error {
			defer lf.Close()
			if err := tw.WriteHeader(&tar.Header{
				Name: "blobs/sha256/" + l.desc.Digest.Hex(),
				Size: l.desc.Size,
				Mode: 0444,
			}); err != nil {
				return err
			}
			_, err := io.Copy(tw, lf)
			return err
		}()
		if err != nil {
			return err
		}
	}

	if err := addBytes("blobs/sha256/"+configDesc.Digest.Hex(), configJSON); err != nil {
		return err
	}
	if err := addBytes("blobs/sha256/"+manifestDesc.Digest.Hex(), manifestJSON); err != nil {
		return err
	}
	return addBytes("index.json", indexJSON)
}

// hashBlob computes the compressed blob digest (for the layer descriptor) and
// the uncompressed tar digest (for the image config DiffID) in a single pass.
func hashBlob(path string) (layerInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return layerInfo{}, err
	}
	defer f.Close()

	compHash := sha256.New()
	cr := &countingReader{r: io.TeeReader(f, compHash)}

	zr, err := zstd.NewReader(cr)
	if err != nil {
		return layerInfo{}, fmt.Errorf("opening zstd stream: %w", err)
	}
	defer zr.Close()

	diffHash := sha256.New()
	if _, err := io.Copy(diffHash, zr); err != nil {
		return layerInfo{}, fmt.Errorf("hashing: %w", err)
	}

	blobDigest := digest.NewDigest("sha256", compHash)
	return layerInfo{
		desc: ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageLayerZstd,
			Digest:    blobDigest,
			Size:      cr.n,
		},
		diffID: digest.NewDigest("sha256", diffHash),
		path:   path,
	}, nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func ensureTag(ref string) string {
	last := strings.LastIndex(ref, "/")
	if !strings.ContainsAny(ref[last+1:], ":@") {
		return ref + ":latest"
	}
	return ref
}
