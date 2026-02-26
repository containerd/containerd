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
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time" // Added for pullFuzzContext

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/pkg/namespaces" // Added for pullFuzzContext
	"github.com/containerd/platforms"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	containerd "github.com/containerd/containerd/v2/client"
)

// mockResolver implements the remotes.Resolver interface for fuzzing.
type mockResolver struct {
	manifest ocispec.Manifest
	blobs    map[digest.Digest][]byte
}

func (r *mockResolver) Resolve(ctx context.Context, ref string) (string, ocispec.Descriptor, error) {
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    r.manifest.Config.Digest,
		Size:      r.manifest.Config.Size,
	}
	return "fuzz-ref", desc, nil
}

func (r *mockResolver) Fetcher(ctx context.Context, ref string) (remotes.Fetcher, error) {
	return &mockFetcher{
		blobs: r.blobs,
	}, nil
}

func (r *mockResolver) Pusher(ctx context.Context, ref string) (remotes.Pusher, error) {
	return nil, fmt.Errorf("Pusher not implemented in mockResolver")
}

// mockFetcher implements the remotes.Fetcher interface for fuzzing.
type mockFetcher struct {
	blobs map[digest.Digest][]byte
}

func (f *mockFetcher) Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	data, ok := f.blobs[desc.Digest]
	if !ok {
		// Return a reader with no data for digests that don't exist
		return io.NopCloser(bytes.NewReader([]byte{})), nil
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// pullFuzzContext provides a context for fuzzing operations.
// Renamed from fuzzContext to avoid redeclaration conflicts with other fuzz tests.
func pullFuzzContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	ctx = namespaces.WithNamespace(ctx, "fuzzing")
	return ctx, cancel
}

func FuzzPull(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		initDaemon.Do(startDaemon)

		client, err := containerd.New(defaultAddress)
		if err != nil {
			// This can happen if the daemon is not ready
			return
		}
		defer client.Close()

		fuzzer := fuzz.NewConsumer(data)

		// Create a manifest from the fuzzer data
		manifest := ocispec.Manifest{}
		err = fuzzer.GenerateStruct(&manifest)
		if err != nil {
			return
		}

		// Create a set of blobs from the fuzzer data
		blobs := make(map[digest.Digest][]byte)
		blobCount, err := fuzzer.GetInt()
		if err != nil {
			return
		}
		for i := 0; i < blobCount%10; i++ {
			blobData, err := fuzzer.GetBytes()
			if err != nil {
				return
			}
			if len(blobData) == 0 {
				continue
			}
			dgst := digest.FromBytes(blobData)
			blobs[dgst] = blobData
		}

		// Ensure the manifest config digest exists in our blobs
		configData, err := fuzzer.GetBytes()
		if err != nil {
			return
		}
		if len(configData) > 0 {
			manifest.Config.Digest = digest.FromBytes(configData)
			manifest.Config.Size = int64(len(configData))
			blobs[manifest.Config.Digest] = configData
		}

		// Create the mock resolver
		resolver := &mockResolver{
			manifest: manifest,
			blobs:    blobs,
		}

		ctx, cancel := pullFuzzContext()
		defer cancel()

		// Call client.Pull with our mock resolver.
		// The image reference string "fuzz-image" is arbitrary as our resolver will ignore it.
		_, _ = client.Pull(ctx, "fuzz-image", containerd.WithResolver(resolver), containerd.WithPlatformMatcher(platforms.All))
	})
}
