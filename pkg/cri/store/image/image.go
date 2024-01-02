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

	"github.com/containerd/containerd/v2/content"
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/images"
	"github.com/containerd/containerd/v2/images/usage"
	ctrdlabels "github.com/containerd/containerd/v2/labels"
	"github.com/containerd/containerd/v2/pkg/cri/labels"
	"github.com/containerd/containerd/v2/pkg/cri/util"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/log"
	docker "github.com/distribution/reference"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/opencontainers/go-digest"
	imagedigest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/go-digest/digestset"
	imageidentity "github.com/opencontainers/image-spec/identity"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
)

// refName:runtimeHandler OR
// imageID:runtimeHandler
//const keyFormat = "%s:%s"

type RefCacheKey struct {
	// Id of the image. Normally the digest of image config.
	Ref string
	// runtimehandler used for pulling this image
	RuntimeHandler string
}

type ImageIDKey struct {
	// Id of the image. Normally the digest of image config.
	ID string
	// runtimehandler used for pulling this image
	RuntimeHandler string
}

// Image contains all resources associated with the image. All fields
// MUST not be mutated directly after created.
type Image struct {
	Key ImageIDKey
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

// InfoProvider provides both content and info about content
type InfoProvider interface {
	content.Provider
	Info(ctx context.Context, dgst digest.Digest) (content.Info, error)
}

// Store stores all images.
type Store struct {
	lock sync.RWMutex
	// refCache is a containerd image reference key to image id key cache.
	// refCache is indexed by ref,key to image.ID,runtimehandler
	refCache map[RefCacheKey]ImageIDKey

	// images is the local image store
	images images.Store

	// content provider
	provider InfoProvider

	// platform represents the currently supported platform for images
	// TODO: Make this store multi-platform
	platform platforms.MatchComparer // TODO (kiashok): remove?

	// initializes matchComparer for each runtime class using default platform or
	// guestPlatform specified for the runtime handler (see pkg/cri/config/config.go).
	runtimeHandlerToPlatformMap map[string]imagespec.Platform

	// store is the internal image store indexed by image id.
	store *store
}

type store struct {
	lock sync.RWMutex
	// images is indexed by ImageKey - a combination of imageID, runtimeHandler used to pull the image
	images    map[ImageIDKey]Image
	digestSet *digestset.Set
	// imageID cache is to keep track of number of images in the CRI store that
	// are currently using the same digest. With image pull per runtime class,
	// the same image can now exists for multiple different runtime handlers
	// imageID -> runtimeHandlers referencing the image
	digestReferences map[string]sets.Set[ImageIDKey]
	// imageDigest -> Key
	pinnedRefs map[string]sets.Set[RefCacheKey]
}

// NewStore creates an image store.
func NewStore(img images.Store, provider InfoProvider, platform platforms.MatchComparer, runtimeHandlerToPlatformMap map[string]imagespec.Platform) *Store {
	return &Store{
		refCache:                    make(map[RefCacheKey]ImageIDKey),
		images:                      img,
		provider:                    provider,
		platform:                    platform,
		runtimeHandlerToPlatformMap: runtimeHandlerToPlatformMap,
		store: &store{
			images:           make(map[ImageIDKey]Image),
			digestSet:        digestset.NewSet(),
			digestReferences: make(map[string]sets.Set[ImageIDKey]),
			pinnedRefs:       make(map[string]sets.Set[RefCacheKey]),
		},
	}
}

func (s *Store) deleteRefWithRuntimeHandler(refCacheKey RefCacheKey) {
	//s.lock.Lock()
	//	defer s.lock.Unlock()

	// Remove the reference from the store.
	s.store.delete(s.refCache[refCacheKey].ID, refCacheKey)
	delete(s.refCache, refCacheKey)
	//delete(s.imageIDReferences, runtimeHandler)
}

// Update updates cache for a reference.
func (s *Store) Update(ctx context.Context, ref, runtimeHandler string) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	i, err := s.images.Get(ctx, ref)
	if err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("get image from containerd: %w", err)
	}
	// if the runtimeHandler label does not exist on the image, it means that the
	// runtime Handler does not reference this image. Therefore remove this ref from the
	// CRI image store
	runtimeHandlerImageLabelKey := fmt.Sprintf(ctrdlabels.RuntimeHandlerLabelFormat, ctrdlabels.RuntimeHandlerLabelPrefix, runtimeHandler)
	if err == nil && i.Labels[runtimeHandlerImageLabelKey] == "" {
		refCacheKey := RefCacheKey{Ref: ref, RuntimeHandler: runtimeHandler}
		_, ok := s.refCache[refCacheKey]
		if ok {
			s.deleteRefWithRuntimeHandler(refCacheKey)
		}
		return nil
	}

	var img *Image
	if err == nil {
		img, err = s.getImage(ctx, i, runtimeHandler)
		if err != nil {
			return fmt.Errorf("get image info from containerd: %w", err)
		}
	}
	return s.update(ref, runtimeHandler, img)
}

// update updates the internal cache. img == nil means that
// the image does not exist in containerd.
func (s *Store) update(ref string, runtimeHandler string, img *Image) error {
	refCacheKey := RefCacheKey{Ref: ref, RuntimeHandler: runtimeHandler}
	//refCacheKey := fmt.Sprintf(imageKeyFormat, ref, runtimeHandler)
	oldImageIDKey, oldExist := s.refCache[refCacheKey]

	if img == nil {
		// The image reference doesn't exist in containerd.
		if oldExist {
			// Remove the reference from the store.
			s.store.delete(oldImageIDKey.ID, refCacheKey)
			delete(s.refCache, refCacheKey)
			//delete(s.imageIDReferences, runtimeHandler)
		}
		return nil
	}

	if oldExist {
		if oldImageIDKey.ID == img.Key.ID && oldImageIDKey.RuntimeHandler == runtimeHandler {
			if s.store.isPinned(oldImageIDKey.ID, refCacheKey) == img.Pinned {
				return nil
			}
			if img.Pinned {
				return s.store.pin(oldImageIDKey.ID, refCacheKey)
			}
			return s.store.unpin(oldImageIDKey.ID, refCacheKey)
		}
		// Updated. Remove tag from old image.
		s.store.delete(oldImageIDKey.ID, refCacheKey)
	}
	// New image. Add new image.
	//imageIDKey := ImageIDKey{ID: img.ID, RuntimeHandler: img.RuntimeHandler}
	s.refCache[refCacheKey] = img.Key
	return s.store.add(*img)
}

// getImage gets image information from containerd for current platform.
func (s *Store) getImage(ctx context.Context, i images.Image, runtimeHandler string) (*Image, error) {
	ocispecPlatform := s.runtimeHandlerToPlatformMap[runtimeHandler]
	platform := platforms.Only(ocispecPlatform)

	diffIDs, err := i.RootFS(ctx, s.provider, platform)
	if err != nil {
		return nil, fmt.Errorf("get image diffIDs: %w", err)
	}
	chainID := imageidentity.ChainID(diffIDs)

	size, err := usage.CalculateImageUsage(ctx, i, s.provider, usage.WithManifestLimit(platform, 1), usage.WithManifestUsage())
	if err != nil {
		return nil, fmt.Errorf("get image compressed resource size: %w", err)
	}

	desc, err := i.Config(ctx, s.provider, platform)
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

	imageKey := ImageIDKey{
		ID:             id,
		RuntimeHandler: runtimeHandler,
	}
	return &Image{
		Key:        imageKey,
		References: []string{i.Name},
		ChainID:    chainID.String(),
		Size:       size,
		ImageSpec:  spec,
		Pinned:     pinned,
	}, nil

}

// Resolve resolves a image reference to image id.
func (s *Store) Resolve(ref, runtimeHandler string) (string, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	refCacheKey := RefCacheKey{Ref: ref, RuntimeHandler: runtimeHandler}
	imageIDKey, ok := s.refCache[refCacheKey]
	if !ok {
		return "", errdefs.ErrNotFound
	}
	log.G(context.Background()).Debugf("!! Store.Resolve(), returning %v", imageIDKey.ID)
	return imageIDKey.ID, nil
}

// Get gets image metadata by image id. The id can be truncated.
// Returns various validation errors if the image id is invalid.
// Returns errdefs.ErrNotFound if the image doesn't exist.
func (s *Store) Get(id string, runtimeHandler string) (Image, error) {
	return s.store.get(id, runtimeHandler)
}

// List lists all images.
func (s *Store) List() []Image {
	return s.store.list()
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
	// pass the img.ID
	if _, err := s.digestSet.Lookup(img.Key.ID); err != nil {
		if err != digestset.ErrDigestNotFound {
			return err
		}
		if err := s.digestSet.Add(imagedigest.Digest(img.Key.ID)); err != nil {
			return err
		}
	}

	if len(s.digestReferences[img.Key.ID]) == 0 {
		s.digestReferences[img.Key.ID] = sets.New(img.Key)
	} else {
		s.digestReferences[img.Key.ID].Insert(img.Key)
	}

	if img.Pinned {
		var refCacheKeys []RefCacheKey
		for _, references := range img.References {
			refCacheKeys = append(refCacheKeys, RefCacheKey{Ref: references, RuntimeHandler: img.Key.RuntimeHandler})
		}
		if refs := s.pinnedRefs[img.Key.ID]; refs == nil {
			s.pinnedRefs[img.Key.ID] = sets.New(refCacheKeys...)
		} else {
			refs.Insert(refCacheKeys...)
		}
	}

	i, ok := s.images[img.Key]
	if !ok {
		// If the image doesn't exist, add it.
		s.images[img.Key] = img
		return nil
	}
	// Or else, merge and sort the references.
	i.References = docker.Sort(util.MergeStringSlices(i.References, img.References))
	i.Pinned = i.Pinned || img.Pinned
	s.images[img.Key] = i
	return nil
}

func (s *store) isPinned(imageID string, refCacheKey RefCacheKey) bool {
	s.lock.RLock()
	defer s.lock.RUnlock()
	digest, err := s.digestSet.Lookup(imageID)
	if err != nil {
		return false
	}
	refs := s.pinnedRefs[digest.String()]
	return refs != nil && refs.Has(refCacheKey)
}

func (s *store) pin(imageID string, refCacheKey RefCacheKey) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	digest, err := s.digestSet.Lookup(imageID)
	if err != nil {
		if err == digestset.ErrDigestNotFound {
			err = errdefs.ErrNotFound
		}
		return err
	}
	imageIDKey := ImageIDKey{ID: digest.String(), RuntimeHandler: refCacheKey.RuntimeHandler}
	i, ok := s.images[imageIDKey]
	if !ok {
		return errdefs.ErrNotFound
	}

	if refs := s.pinnedRefs[digest.String()]; refs == nil {
		s.pinnedRefs[digest.String()] = sets.New(refCacheKey)
	} else {
		refs.Insert(refCacheKey)
	}
	i.Pinned = true
	s.images[imageIDKey] = i
	return nil
}

func (s *store) unpin(imageID string, refCacheKey RefCacheKey) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	digest, err := s.digestSet.Lookup(imageID)
	if err != nil {
		if err == digestset.ErrDigestNotFound {
			err = errdefs.ErrNotFound
		}
		return err
	}
	imageIDKey := ImageIDKey{ID: digest.String(), RuntimeHandler: refCacheKey.RuntimeHandler}
	i, ok := s.images[imageIDKey]
	if !ok {
		return errdefs.ErrNotFound
	}

	refs := s.pinnedRefs[digest.String()]
	if refs == nil {
		return nil
	}
	if refs.Delete(refCacheKey); len(refs) > 0 {
		return nil
	}

	// delete unpinned image, we only need to keep the pinned
	// entries in the map
	delete(s.pinnedRefs, digest.String())
	i.Pinned = false
	s.images[imageIDKey] = i
	return nil
}

func (s *store) get(imageID, runtimeHandler string) (Image, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	digest, err := s.digestSet.Lookup(imageID)
	if err != nil {
		if err == digestset.ErrDigestNotFound {
			err = errdefs.ErrNotFound
		}
		return Image{}, err
	}
	imageIDKey := ImageIDKey{ID: digest.String(), RuntimeHandler: runtimeHandler}
	if i, ok := s.images[imageIDKey]; ok {
		return i, nil
	}
	return Image{}, errdefs.ErrNotFound
}

func (s *store) delete(imageID string, refCacheKey RefCacheKey) {
	s.lock.Lock()
	defer s.lock.Unlock()
	digest, err := s.digestSet.Lookup(imageID)
	if err != nil {
		// Note: The idIndex.Delete and delete doesn't handle truncated index.
		// So we need to return if there are error.
		return
	}
	imageIDKey := ImageIDKey{ID: digest.String(), RuntimeHandler: refCacheKey.RuntimeHandler}

	if refs := s.pinnedRefs[digest.String()]; refs != nil {
		refs.Delete(refCacheKey)
	}

	delete(s.images, imageIDKey)
	delete(s.digestReferences[imageIDKey.ID], imageIDKey)

	if len(s.pinnedRefs[digest.String()]) == 0 {
		delete(s.pinnedRefs, digest.String())
	}
	// Images can now be pulled per runtime handler. so every image id could
	// exist for more than one runtimehandler as well.
	// So only remove the entry from digest set if it is not referenced any more by any runtimeHandler
	if len(s.digestReferences[imageIDKey.ID]) == 0 {
		s.digestSet.Remove(digest)
		delete(s.pinnedRefs, digest.String())
	}
	return
}
