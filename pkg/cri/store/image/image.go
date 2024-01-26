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

package image

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/usage"
	"github.com/containerd/containerd/v2/pkg/cri/labels"
	"github.com/containerd/containerd/v2/pkg/cri/util"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	docker "github.com/distribution/reference"
	"k8s.io/apimachinery/pkg/util/sets"

	imagedigest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/go-digest/digestset"
	imageidentity "github.com/opencontainers/image-spec/identity"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Image contains all resources associated with the image. All fields
// MUST not be mutated directly after created.
type Image struct {
	// Id of the image. Normally the digest of image config.
	ID string
	// References are references to the image, e.g. RepoTag and RepoDigest.
	References []string
	// ChainID is the chainID of the image.
	ChainID string
	// Size is the compressed size of the image.
	Size int64
	// ImageSpec is the oci image structure which describes basic information about the image.
	ImageSpec imagespec.Image
	// Pinned image to prevent it from garbage collection
	Pinned bool
}

// Getter is used to get images but does not make changes
type Getter interface {
	Get(ctx context.Context, name string) (images.Image, error)
}

// Store stores all images.
type Store struct {
	lock sync.RWMutex
	// refCache is a containerd image reference to image id cache.
	refCache map[string]string

	// images is the local image store
	images Getter

	// content provider
	provider content.InfoReaderProvider

	// platform represents the currently supported platform for images
	// TODO: Make this store multi-platform
	platform platforms.MatchComparer

	// store is the internal image store indexed by image id.
	store *store
}

// NewStore creates an image store.
func NewStore(img Getter, provider content.InfoReaderProvider, platform platforms.MatchComparer) *Store {
	return &Store{
		refCache: make(map[string]string),
		images:   img,
		provider: provider,
		platform: platform,
		store: &store{
			images:     make(map[string]Image),
			digestSet:  digestset.NewSet(),
			pinnedRefs: make(map[string]sets.Set[string]),
		},
	}
}

// Update updates cache for a reference.
func (s *Store) Update(ctx context.Context, ref string) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	i, err := s.images.Get(ctx, ref)
	if err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("get image from containerd: %w", err)
	}

	var img *Image
	if err == nil {
		img, err = s.getImage(ctx, i)
		if err != nil {
			return fmt.Errorf("get image info from containerd: %w", err)
		}
	}
	return s.update(ref, img)
}

// update updates the internal cache. img == nil means that
// the image does not exist in containerd.
func (s *Store) update(ref string, img *Image) error {
	oldID, oldExist := s.refCache[ref]
	if img == nil {
		// The image reference doesn't exist in containerd.
		if oldExist {
			// Remove the reference from the store.
			s.store.delete(oldID, ref)
			delete(s.refCache, ref)
		}
		return nil
	}
	if oldExist {
		if oldID == img.ID {
			if s.store.isPinned(img.ID, ref) == img.Pinned {
				return nil
			}
			if img.Pinned {
				return s.store.pin(img.ID, ref)
			}
			return s.store.unpin(img.ID, ref)
		}
		// Updated. Remove tag from old image.
		s.store.delete(oldID, ref)
	}
	// New image. Add new image.
	s.refCache[ref] = img.ID
	return s.store.add(*img)
}

// getImage gets image information from containerd for current platform.
func (s *Store) getImage(ctx context.Context, i images.Image) (*Image, error) {
	diffIDs, err := i.RootFS(ctx, s.provider, s.platform)
	if err != nil {
		return nil, fmt.Errorf("get image diffIDs: %w", err)
	}
	chainID := imageidentity.ChainID(diffIDs)

	size, err := usage.CalculateImageUsage(ctx, i, s.provider, usage.WithManifestLimit(s.platform, 1), usage.WithManifestUsage())
	if err != nil {
		return nil, fmt.Errorf("get image compressed resource size: %w", err)
	}

	desc, err := i.Config(ctx, s.provider, s.platform)
	if err != nil {
		return nil, fmt.Errorf("get image config descriptor: %w", err)
	}
	id := desc.Digest.String()

	blob, err := content.ReadBlob(ctx, s.provider, desc)
	if err != nil {
		return nil, fmt.Errorf("read image config from content store: %w", err)
	}

	var spec imagespec.Image
	if err := json.Unmarshal(blob, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal image config %s: %w", blob, err)
	}

	pinned := i.Labels[labels.PinnedImageLabelKey] == labels.PinnedImageLabelValue

	return &Image{
		ID:         id,
		References: []string{i.Name},
		ChainID:    chainID.String(),
		Size:       size,
		ImageSpec:  spec,
		Pinned:     pinned,
	}, nil

}

// Resolve resolves a image reference to image id.
func (s *Store) Resolve(ref string) (string, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	id, ok := s.refCache[ref]
	if !ok {
		return "", errdefs.ErrNotFound
	}
	return id, nil
}

// Get gets image metadata by image id. The id can be truncated.
// Returns various validation errors if the image id is invalid.
// Returns errdefs.ErrNotFound if the image doesn't exist.
func (s *Store) Get(id string) (Image, error) {
	return s.store.get(id)
}

// List lists all images.
func (s *Store) List() []Image {
	return s.store.list()
}

type store struct {
	lock       sync.RWMutex
	images     map[string]Image
	digestSet  *digestset.Set
	pinnedRefs map[string]sets.Set[string]
}

func (s *store) list() []Image {
	s.lock.RLock()
	defer s.lock.RUnlock()
	var images []Image
	for _, i := range s.images {
		images = append(images, i)
	}
	return images
}

func (s *store) add(img Image) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	if _, err := s.digestSet.Lookup(img.ID); err != nil {
		if err != digestset.ErrDigestNotFound {
			return err
		}
		if err := s.digestSet.Add(imagedigest.Digest(img.ID)); err != nil {
			return err
		}
	}

	if img.Pinned {
		if refs := s.pinnedRefs[img.ID]; refs == nil {
			s.pinnedRefs[img.ID] = sets.New(img.References...)
		} else {
			refs.Insert(img.References...)
		}
	}

	i, ok := s.images[img.ID]
	if !ok {
		// If the image doesn't exist, add it.
		s.images[img.ID] = img
		return nil
	}
	// Or else, merge and sort the references.
	i.References = docker.Sort(util.MergeStringSlices(i.References, img.References))
	i.Pinned = i.Pinned || img.Pinned
	s.images[img.ID] = i
	return nil
}

func (s *store) isPinned(id, ref string) bool {
	s.lock.RLock()
	defer s.lock.RUnlock()
	digest, err := s.digestSet.Lookup(id)
	if err != nil {
		return false
	}
	refs := s.pinnedRefs[digest.String()]
	return refs != nil && refs.Has(ref)
}

func (s *store) pin(id, ref string) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	digest, err := s.digestSet.Lookup(id)
	if err != nil {
		if err == digestset.ErrDigestNotFound {
			err = errdefs.ErrNotFound
		}
		return err
	}
	i, ok := s.images[digest.String()]
	if !ok {
		return errdefs.ErrNotFound
	}

	if refs := s.pinnedRefs[digest.String()]; refs == nil {
		s.pinnedRefs[digest.String()] = sets.New(ref)
	} else {
		refs.Insert(ref)
	}
	i.Pinned = true
	s.images[digest.String()] = i
	return nil
}

func (s *store) unpin(id, ref string) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	digest, err := s.digestSet.Lookup(id)
	if err != nil {
		if err == digestset.ErrDigestNotFound {
			err = errdefs.ErrNotFound
		}
		return err
	}
	i, ok := s.images[digest.String()]
	if !ok {
		return errdefs.ErrNotFound
	}

	refs := s.pinnedRefs[digest.String()]
	if refs == nil {
		return nil
	}
	if refs.Delete(ref); len(refs) > 0 {
		return nil
	}

	// delete unpinned image, we only need to keep the pinned
	// entries in the map
	delete(s.pinnedRefs, digest.String())
	i.Pinned = false
	s.images[digest.String()] = i
	return nil
}

func (s *store) get(id string) (Image, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	digest, err := s.digestSet.Lookup(id)
	if err != nil {
		if err == digestset.ErrDigestNotFound {
			err = errdefs.ErrNotFound
		}
		return Image{}, err
	}
	if i, ok := s.images[digest.String()]; ok {
		return i, nil
	}
	return Image{}, errdefs.ErrNotFound
}

func (s *store) delete(id, ref string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	digest, err := s.digestSet.Lookup(id)
	if err != nil {
		// Note: The idIndex.Delete and delete doesn't handle truncated index.
		// So we need to return if there are error.
		return
	}
	i, ok := s.images[digest.String()]
	if !ok {
		return
	}
	i.References = util.SubtractStringSlice(i.References, ref)
	if len(i.References) != 0 {
		if refs := s.pinnedRefs[digest.String()]; refs != nil {
			if refs.Delete(ref); len(refs) == 0 {
				i.Pinned = false
				// delete unpinned image, we only need to keep the pinned
				// entries in the map
				delete(s.pinnedRefs, digest.String())
			}
		}

		s.images[digest.String()] = i
		return
	}
	// Remove the image if it is not referenced any more.
	s.digestSet.Remove(digest)
	delete(s.images, digest.String())
	delete(s.pinnedRefs, digest.String())
}
