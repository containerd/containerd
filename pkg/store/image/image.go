/*
Copyright 2017 The Kubernetes Authors.

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

package image

import (
	"sync"

	"github.com/containerd/containerd"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/store"
)

// Image contains all resources associated with the image. All fields
// MUST not be mutated directly after created.
type Image struct {
	// Id of the image. Normally the digest of image config.
	ID string
	// Other names by which this image is known.
	RepoTags []string
	// Digests by which this image is known.
	RepoDigests []string
	// ChainID is the chainID of the image.
	ChainID string
	// Size is the compressed size of the image.
	Size int64
	// Config is the oci image config of the image.
	Config *imagespec.ImageConfig
	// Containerd image reference
	Image containerd.Image
}

// Store stores all images.
type Store struct {
	lock   sync.RWMutex
	images map[string]Image
	// TODO(random-liu): Add trunc index.
}

// LoadStore loads images from runtime.
// TODO(random-liu): Implement LoadStore.
func LoadStore() *Store { return nil }

// NewStore creates an image store.
func NewStore() *Store {
	return &Store{images: make(map[string]Image)}
}

// Add an image into the store.
func (s *Store) Add(img Image) {
	s.lock.Lock()
	defer s.lock.Unlock()
	i, ok := s.images[img.ID]
	if !ok {
		// If the image doesn't exist, add it.
		s.images[img.ID] = img
		return
	}
	// Or else, merge the repo tags/digests.
	i.RepoTags = mergeStringSlices(i.RepoTags, img.RepoTags)
	i.RepoDigests = mergeStringSlices(i.RepoDigests, img.RepoDigests)
	s.images[img.ID] = i
}

// Get returns the image with specified id. Returns store.ErrNotExist if the
// image doesn't exist.
func (s *Store) Get(id string) (Image, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	if i, ok := s.images[id]; ok {
		return i, nil
	}
	return Image{}, store.ErrNotExist
}

// List lists all images.
func (s *Store) List() []Image {
	s.lock.RLock()
	defer s.lock.RUnlock()
	var images []Image
	for _, sb := range s.images {
		images = append(images, sb)
	}
	return images
}

// Delete deletes the image with specified id.
func (s *Store) Delete(id string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	delete(s.images, id)
}

// mergeStringSlices merges 2 string slices into one and remove duplicated elements.
func mergeStringSlices(a []string, b []string) []string {
	set := map[string]struct{}{}
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		set[s] = struct{}{}
	}
	var ss []string
	for s := range set {
		ss = append(ss, s)
	}
	return ss
}
