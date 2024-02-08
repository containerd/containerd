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

package imagetest

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
	"sync"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Content represents a piece of image content in the content store along with
// its relevant children and size.
type Content struct {
	Descriptor ocispec.Descriptor
	Labels     map[string]string
	Size       Size
	Children   []Content
}

// ContentStore is a temporary content store which provides simple
// helper functions for quickly altering content in the store.
// Directly modifying the content store without using the helper functions
// may result in out of sync test content values, be careful of this
// when writing tests.
type ContentStore struct {
	content.Store

	ctx context.Context
	t   *testing.T
}

// NewContentStore creates a new content store in the testing's temporary directory
func NewContentStore(ctx context.Context, t *testing.T) ContentStore {
	cs, err := local.NewLabeledStore(t.TempDir(), newMemoryLabelStore())
	if err != nil {
		t.Fatal(err)
	}

	return ContentStore{
		Store: cs,
		ctx:   ctx,
		t:     t,
	}
}

// Index creates an index with the provided manifests and stores it
// in the content store.
func (tc ContentStore) Index(manifests ...Content) Content {
	var index ocispec.Index
	for _, m := range manifests {
		index.Manifests = append(index.Manifests, m.Descriptor)
	}

	idx := tc.JSONObject(ocispec.MediaTypeImageIndex, index)
	idx.Children = manifests
	return idx

}

// Manifest creates a manifest with the given config and layers then
// stores it in the content store.
func (tc ContentStore) Manifest(config Content, layers ...Content) Content {
	var manifest ocispec.Manifest
	manifest.Config = config.Descriptor
	for _, l := range layers {
		manifest.Layers = append(manifest.Layers, l.Descriptor)
	}
	m := tc.JSONObject(ocispec.MediaTypeImageManifest, manifest)
	m.Children = append(m.Children, config)
	m.Children = append(m.Children, layers...)
	return m
}

// Blob creates a generic blob with the given data and media type
// and stores the data in the content store.
func (tc ContentStore) Blob(mediaType string, data []byte) Content {
	tc.t.Helper()

	descriptor := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.SHA256.FromBytes(data),
		Size:      int64(len(data)),
	}

	ref := string(descriptor.Digest) // TODO: Add random component?
	if err := content.WriteBlob(tc.ctx, tc.Store, ref, bytes.NewReader(data), descriptor); err != nil {
		tc.t.Fatal(err)
	}

	return Content{
		Descriptor: descriptor,
		Size: Size{
			Manifest: descriptor.Size,
			Content:  descriptor.Size,
		},
	}

}

// RandomBlob creates a blob object in the content store with random data.
func (tc ContentStore) RandomBlob(mediaType string, n int) Content {
	tc.t.Helper()

	data := make([]byte, int64(n))
	rand.New(rand.NewSource(int64(n))).Read(data)

	return tc.Blob(mediaType, data)
}

// JSONObject creates an object in the content store by first marshaling
// to JSON and then storing the data.
func (tc ContentStore) JSONObject(mediaType string, i interface{}) Content {
	tc.t.Helper()

	data, err := json.Marshal(i)
	if err != nil {
		tc.t.Fatal(err)
	}

	return tc.Blob(mediaType, data)
}

// Walk walks all the children of the provided content and calls the provided
// function with the associated content store.
// Walk can be used to update an object and reflect that change in the content
// store.
func (tc ContentStore) Walk(c Content, fn func(context.Context, *Content, content.Store) error) Content {
	tc.t.Helper()

	if err := fn(tc.ctx, &c, tc.Store); err != nil {
		tc.t.Fatal(err)
	}

	if len(c.Children) > 0 {
		children := make([]Content, len(c.Children))
		for i, child := range c.Children {
			children[i] = tc.Walk(child, fn)
		}
		c.Children = children
	}

	return c
}

// AddPlatform alters the content desciptor by setting the platform
func AddPlatform(c Content, p ocispec.Platform) Content {
	c.Descriptor.Platform = &p
	return c
}

// LimitChildren limits the amount of children in the content.
// This function is non-recursive and uses the natural ordering.
func LimitChildren(c Content, limit int) Content {
	if images.IsIndexType(c.Descriptor.MediaType) {
		if len(c.Children) > limit {
			c.Children = c.Children[0:limit]
		}
	}
	return c
}

// ContentCreator is a simple interface for generating content for tests
type ContentCreator func(ContentStore) Content

// SimpleManifest generates a simple manifest with small config and random layer
// The layer produced is not a valid compressed tar, do not unpack it.
func SimpleManifest(layerSize int) ContentCreator {
	return func(tc ContentStore) Content {
		config := ocispec.ImageConfig{
			Env: []string{"random"}, // TODO: Make random string
		}
		return tc.Manifest(
			tc.JSONObject(ocispec.MediaTypeImageConfig, config),
			tc.RandomBlob(ocispec.MediaTypeImageLayerGzip, layerSize))
	}
}

// SimpleIndex generates a simple index with the number of simple
// manifests specified.
func SimpleIndex(manifests, layerSize int) ContentCreator {
	manifestFn := SimpleManifest(layerSize)
	return func(tc ContentStore) Content {
		var m []Content
		for i := 0; i < manifests; i++ {
			m = append(m, manifestFn(tc))
		}
		return tc.Index(m...)
	}
}

// StripLayers deletes all layer content from the content store
// and updates the content size to 0.
func StripLayers(cc ContentCreator) ContentCreator {
	return func(tc ContentStore) Content {
		return tc.Walk(cc(tc), func(ctx context.Context, c *Content, store content.Store) error {
			if images.IsLayerType(c.Descriptor.MediaType) {
				if err := store.Delete(tc.ctx, c.Descriptor.Digest); err != nil {
					return err
				}
				c.Size.Content = 0
			}
			return nil
		})

	}
}

type memoryLabelStore struct {
	l      sync.Mutex
	labels map[digest.Digest]map[string]string
}

// newMemoryLabelStore creates an inmemory label store for testing
func newMemoryLabelStore() local.LabelStore {
	return &memoryLabelStore{
		labels: map[digest.Digest]map[string]string{},
	}
}

func (mls *memoryLabelStore) Get(d digest.Digest) (map[string]string, error) {
	mls.l.Lock()
	labels := mls.labels[d]
	mls.l.Unlock()

	return labels, nil
}

func (mls *memoryLabelStore) Set(d digest.Digest, labels map[string]string) error {
	mls.l.Lock()
	mls.labels[d] = labels
	mls.l.Unlock()

	return nil
}

func (mls *memoryLabelStore) Update(d digest.Digest, update map[string]string) (map[string]string, error) {
	mls.l.Lock()
	labels, ok := mls.labels[d]
	if !ok {
		labels = map[string]string{}
	}
	for k, v := range update {
		if v == "" {
			delete(labels, k)
		} else {
			labels[k] = v
		}
	}
	mls.labels[d] = labels
	mls.l.Unlock()

	return labels, nil
}
