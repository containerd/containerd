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
	"github.com/docker/distribution/digestset"
	godigest "github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/cri/pkg/store"
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
	// ImageSpec is the oci image structure which describes basic information about the image.
	ImageSpec imagespec.Image
	// Containerd image reference
	Image containerd.Image
}

// Store stores all images.
type Store struct {
	lock      sync.RWMutex
	images    map[string]Image
	digestSet *digestset.Set
}

// NewStore creates an image store.
func NewStore() *Store {
	return &Store{
		images:    make(map[string]Image),
		digestSet: digestset.NewSet(),
	}
}

// Add an image into the store.
func (s *Store) Add(img Image) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	if _, err := s.digestSet.Lookup(img.ID); err != nil {
		if err != digestset.ErrDigestNotFound {
			return err
		}
		if err := s.digestSet.Add(godigest.Digest(img.ID)); err != nil {
			return err
		}
	}

	i, ok := s.images[img.ID]
	if !ok {
		// If the image doesn't exist, add it.
		s.images[img.ID] = img
		return nil
	}
	// Or else, merge the repo tags/digests.
	i.RepoTags = mergeStringSlices(i.RepoTags, img.RepoTags)
	i.RepoDigests = mergeStringSlices(i.RepoDigests, img.RepoDigests)
	s.images[img.ID] = i
	return nil
}

// Get returns the image with specified id. Returns store.ErrNotExist if the
// image doesn't exist.
func (s *Store) Get(id string) (Image, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	digest, err := s.digestSet.Lookup(id)
	if err != nil {
		if err == digestset.ErrDigestNotFound {
			err = store.ErrNotExist
		}
		return Image{}, err
	}
	if i, ok := s.images[digest.String()]; ok {
		return i, nil
	}
	return Image{}, store.ErrNotExist
}

// List lists all images.
func (s *Store) List() []Image {
	s.lock.RLock()
	defer s.lock.RUnlock()
	var images []Image
	for _, i := range s.images {
		images = append(images, i)
	}
	return images
}

// Delete deletes the image with specified id.
func (s *Store) Delete(id string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	digest, err := s.digestSet.Lookup(id)
	if err != nil {
		// Note: The idIndex.Delete and delete doesn't handle truncated index.
		// So we need to return if there are error.
		return
	}
	s.digestSet.Remove(digest) // nolint: errcheck
	delete(s.images, digest.String())
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
