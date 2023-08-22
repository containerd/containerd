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
	"fmt"
	"sync"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/pkg/cri/labels"
	"github.com/containerd/containerd/pkg/cri/util"
	"github.com/containerd/containerd/reference/docker"

	imagedigest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/go-digest/digestset"
	imageidentity "github.com/opencontainers/image-spec/identity"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
)

// name-runtimehandler
const imageKeyFormat = "%s-%s"

// Image contains all resources associated with the image. All fields
// MUST not be mutated directly after created.
type Image struct {
	// Id of the image. Normally the digest of image config.
	ID string
	// runtime handler used to pull this image.
	RuntimeHandler string
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
	// matchcomparer for each runtime class. For more info, see CriService struct
	platformMatcherMap map[string]platforms.MatchComparer
}

// Store stores all images.
type Store struct {
	lock sync.RWMutex
	// refCache is a containerd image reference to image id cache.
	refCache map[string]string
	// client is the containerd client.
	client *containerd.Client
	// matchcomparer for each runtime class. For more info, see CriService struct
	platformMatcherMap map[string]platforms.MatchComparer
	// store is the internal image store indexed by image id.
	store *store
}

// NewStore creates an image store.
func NewStore(client *containerd.Client, platformMatcherMap map[string]platforms.MatchComparer) *Store {
	return &Store{
		refCache: make(map[string]string),
		client:   client,
		platformMatcherMap: platformMatcherMap,
		store: &store{
			images:    make(map[string]Image),
			digestSet: digestset.NewSet(),
		},
	}
}

// Update updates cache for a reference.
func (s *Store) Update(ctx context.Context, ref string, runtimeHandler string) error { //TODO: pass runtimeHandler and make this a compulsory field eventually
	s.lock.Lock()
	defer s.lock.Unlock()

	log.G(ctx).Debugf("pkg.cri.store Update(), ref is %v, runtimeHdlr is %v", ref, runtimeHandler)
	getImageOpts := []containerd.GetImageOpt{
		containerd.GetImageWithPlatformMatcher(s.platformMatcherMap[runtimeHandler]),
	}
	i, err := s.client.GetImage(ctx, ref, getImageOpts...)
	log.G(ctx).Debugf("pkg.cri.store Update(), containerd.Image is %v, err: %v", i, err)
	if err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("get image from containerd: %w", err)
	}
	var img *Image
	if err == nil {
		img, err = getImage(ctx, i, runtimeHandler)
		log.G(ctx).Debugf("pkg.cri.store Update(), after getImage(), img is %v, err: %v", img, err)
		if err != nil {
			return fmt.Errorf("get image info from containerd: %w", err)
		}
	}
	log.G(ctx).Debugf("pkg.cri.store Update(), img is %v", img)
	return s.update(ref, img, runtimeHandler)
}

// update updates the internal cache. img == nil means that
// the image does not exist in containerd.
func (s *Store) update(ref string, img *Image, runtimeHandler string) error {
	//key := fmt.Sprintf(imageKeyFormat, ref, runtimeHandler)
	oldID, oldExist := s.refCache[ref]
	log.G(context.Background()).Debugf("pkg.cri.store update(), ref %v, oldID is %v, oldExist: %v", ref, oldID, oldExist)
	if img == nil {
		// The image reference doesn't exist in containerd.
		if oldExist {
			// Remove the reference from the store.
			s.store.delete(oldID, ref, runtimeHandler)
			delete(s.refCache, ref)
		}
		return nil
	}
	if oldExist {
		if oldID == img.ID {
			return nil
		}
		// Updated. Remove tag from old image.
		s.store.delete(oldID, ref, img.RuntimeHandler)
	}
	// New image. Add new image.
	log.G(context.Background()).Debugf("!!!! pkg.cri.store update(), adding NEWWWW IMAGE s.refCache[ref] %v = img.ID %v", s.refCache[ref], img.ID)
	s.refCache[ref] = img.ID
	return s.store.add(*img)
}

// getImage gets image information from containerd.
func getImage(ctx context.Context, i containerd.Image, runtimeHandler string) (*Image, error) {
	log.G(ctx).Debugf("pkg.cri.store getImage(), containerd.Image is %v", i)
	// Get image information.
	diffIDs, err := i.RootFS(ctx)
	if err != nil {
		return nil, fmt.Errorf("get image diffIDs: %w", err)
	}
	chainID := imageidentity.ChainID(diffIDs)

	size, err := i.Size(ctx)
	if err != nil {
		return nil, fmt.Errorf("get image compressed resource size: %w", err)
	}

	desc, err := i.Config(ctx)
	if err != nil {
		return nil, fmt.Errorf("get image config descriptor: %w", err)
	}
	id := desc.Digest.String()

	spec, err := i.Spec(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get OCI image spec: %w", err)
	}

	pinned := i.Labels()[labels.PinnedImageLabelKey] == labels.PinnedImageLabelValue

	return &Image{
		ID:         id,
		RuntimeHandler: runtimeHandler,
		References: []string{i.Name()},
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
	log.G(context.Background()).Debugf("pkg.cri.store Reolve(), ref is %v, s.refCache[ref] %v", ref, s.refCache[ref])
	id, ok := s.refCache[ref]
	if !ok {
		return "", errdefs.ErrNotFound
	}
	return id, nil
}

// Get gets image metadata by image id. The id can be truncated.
// Returns various validation errors if the image id is invalid.
// Returns errdefs.ErrNotFound if the image doesn't exist.
func (s *Store) Get(id, runtimeHandler string) (Image, error) {
	return s.store.get(id, runtimeHandler)
}

// List lists all images.
func (s *Store) List() []Image {
	return s.store.list()
}

type store struct {
	lock      sync.RWMutex
	images    map[string]Image
	digestSet *digestset.Set
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

	key := fmt.Sprintf(imageKeyFormat, img.ID, img.RuntimeHandler)
	i, ok := s.images[key]
	if !ok {
		// If the image doesn't exist, add it.
		log.G(context.Background()).Debugf("!! pkg/cri/store add(), key:%v, img %v", key, img)
		s.images[key] = img
		return nil
	}
	// Or else, merge and sort the references.
	i.References = docker.Sort(util.MergeStringSlices(i.References, img.References))
	i.Pinned = i.Pinned || img.Pinned
	log.G(context.Background()).Debugf("!! pkg/cri/store 2 add(), key:%v, img %v", key, img)
	s.images[key] = i
	return nil
}

func (s *store) get(id, runtimeHandler string) (Image, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	digest, err := s.digestSet.Lookup(id)
	if err != nil {
		if err == digestset.ErrDigestNotFound {
			err = errdefs.ErrNotFound
		}
		return Image{}, err
	}

	key := fmt.Sprintf(imageKeyFormat, digest.String(), runtimeHandler)
	if i, ok := s.images[key]; ok {
		return i, nil
	}
	return Image{}, errdefs.ErrNotFound
}

func (s *store) delete(id, ref, runtimeHandler string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	digest, err := s.digestSet.Lookup(id)
	if err != nil {
		// Note: The idIndex.Delete and delete doesn't handle truncated index.
		// So we need to return if there are error.
		return
	}

	key := fmt.Sprintf(imageKeyFormat, digest.String(), runtimeHandler)
	log.G(context.Background()).Debugf("!! pkg/cri/store delete(), key:%v, s.images[key] %v", key, s.images[key])
	i, ok := s.images[key]
	if !ok {
		return
	}
	i.References = util.SubtractStringSlice(i.References, ref)
	if len(i.References) != 0 {
		s.images[key] = i
		return
	}
	// Remove the image if it is not referenced any more.
	s.digestSet.Remove(digest)
	delete(s.images, key)
}
