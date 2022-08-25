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

package client

import (
	"bytes"
	"context"
	"errors"
	"testing"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
)

const (
	maxImports        = 30
	maxNoOfContainers = 50
)

// checkAndDoUnpack checks if an image is unpacked.
// If it is not unpacked, then we may or may not
// unpack it. The fuzzer decides.
func checkAndDoUnpack(image containerd.Image, ctx context.Context, f *fuzz.ConsumeFuzzer) {
	unpacked, err := image.IsUnpacked(ctx, testSnapshotter)
	if err == nil && unpacked {
		shouldUnpack, err := f.GetBool()
		if err == nil && shouldUnpack {
			_ = image.Unpack(ctx, testSnapshotter)
		}
	}
}

// getImage() returns an image from the client.
// The fuzzer decides which image is returned.
func getImage(client *containerd.Client, f *fuzz.ConsumeFuzzer) (containerd.Image, error) {
	images, err := client.ListImages(nil)
	if err != nil {
		return nil, err
	}
	imageIndex, err := f.GetInt()
	if err != nil {
		return nil, err
	}
	image := images[imageIndex%len(images)]
	return image, nil

}

// newContainer creates and returns a container
// The fuzzer decides how the container is created
func newContainer(client *containerd.Client, f *fuzz.ConsumeFuzzer, ctx context.Context) (containerd.Container, error) {
	// determiner determines how we should create the container
	determiner, err := f.GetInt()
	if err != nil {
		return nil, err
	}
	id, err := f.GetString()
	if err != nil {
		return nil, err
	}

	if determiner%1 == 0 {
		// Create a container with oci specs
		spec := &oci.Spec{}
		err = f.GenerateStruct(spec)
		if err != nil {
			return nil, err
		}
		container, err := client.NewContainer(ctx, id,
			containerd.WithSpec(spec))
		if err != nil {
			return nil, err
		}
		return container, nil
	} else if determiner%2 == 0 {
		// Create a container with fuzzed oci specs
		// and an image
		image, err := getImage(client, f)
		if err != nil {
			return nil, err
		}
		// Fuzz a few image APIs
		_, _ = image.Size(ctx)
		checkAndDoUnpack(image, ctx, f)

		spec := &oci.Spec{}
		err = f.GenerateStruct(spec)
		if err != nil {
			return nil, err
		}
		container, err := client.NewContainer(ctx,
			id,
			containerd.WithImage(image),
			containerd.WithSpec(spec))
		if err != nil {
			return nil, err
		}
		return container, nil
	} else {
		// Create a container with an image
		image, err := getImage(client, f)
		if err != nil {
			return nil, err
		}
		// Fuzz a few image APIs
		_, _ = image.Size(ctx)
		checkAndDoUnpack(image, ctx, f)

		container, err := client.NewContainer(ctx,
			id,
			containerd.WithImage(image))
		if err != nil {
			return nil, err
		}
		return container, nil
	}
	return nil, errors.New("Could not create container")
}

func FuzzIntegImport(f *testing.F) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx = namespaces.WithNamespace(ctx, testNamespace)

	client, err := containerd.New(address)
	require.NoError(f, err)
	defer client.Close()

	f.Add(1, 1, []byte{})
	f.Fuzz(func(t *testing.T, noOfImports, noOfContainers int, data []byte) {
		fc := fuzz.NewConsumer(data)

		for i := 0; i < noOfImports%maxImports; i++ {
			// f.TarBytes() returns valid tar bytes.
			tarBytes, err := fc.TarBytes()
			require.NoError(t, err)

			_, _ = client.Import(ctx, bytes.NewReader(tarBytes))
		}

		// Begin create containers:
		existingImages, err := client.ListImages(ctx)
		require.NoError(t, err)

		if len(existingImages) == 0 {
			return
		}
		for i := 0; i < noOfContainers%maxNoOfContainers; i++ {
			container, err := newContainer(client, fc, ctx)
			if err == nil {
				defer container.Delete(ctx, containerd.WithSnapshotCleanup)
			}
		}
	})
}
