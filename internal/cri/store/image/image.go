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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/usage"
	"github.com/containerd/containerd/v2/internal/cri/labels"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	docker "github.com/distribution/reference"

	imagedigest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/go-digest/digestset"
	imageidentity "github.com/opencontainers/image-spec/identity"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

const namePrefix = "io.cri-containerd.image.v1/"

// Name returns the containerd image name for a reference and runtime handler.
// An empty handler preserves the legacy image name.
func Name(ref, handler string) string {
	if handler == "" {
		return ref
	}
	return namePrefix + base64.RawURLEncoding.EncodeToString([]byte(handler)) + "/" + base64.RawURLEncoding.EncodeToString([]byte(ref))
}

// ParseName returns the reference and runtime handler encoded in an image name.
// Names outside the reserved prefix belong to the legacy image store.
func ParseName(name string) (ref, handler string, err error) {
	if !strings.HasPrefix(name, namePrefix) && name != strings.TrimSuffix(namePrefix, "/") {
		return name, "", nil
	}
	ref, handler, err = parseRuntimeName(name)
	if err != nil && strings.HasPrefix(name, namePrefix) {
		if _, parseErr := docker.ParseDockerRef(name); parseErr == nil {
			return name, "", nil
		}
	}
	return ref, handler, err
}

func parseRuntimeName(name string) (ref, handler string, err error) {
	encodedHandler, encodedRef, ok := strings.Cut(strings.TrimPrefix(name, namePrefix), "/")
	if !ok || encodedHandler == "" || encodedRef == "" {
		return "", "", fmt.Errorf("invalid runtime image name %q: %w", name, errdefs.ErrInvalidArgument)
	}
	h, err := base64.RawURLEncoding.DecodeString(encodedHandler)
	if err != nil || base64.RawURLEncoding.EncodeToString(h) != encodedHandler {
		return "", "", fmt.Errorf("invalid runtime handler in image name %q: %w", name, errdefs.ErrInvalidArgument)
	}
	r, err := base64.RawURLEncoding.DecodeString(encodedRef)
	if err != nil || base64.RawURLEncoding.EncodeToString(r) != encodedRef {
		return "", "", fmt.Errorf("invalid reference in image name %q: %w", name, errdefs.ErrInvalidArgument)
	}
	return string(r), string(h), nil
}

// Image contains all resources associated with the image. All fields
// MUST not be mutated directly after created.
type Image struct {
	// Id of the image. Normally the digest of image config.
	ID string
	// RuntimeHandler is the runtime handler owning the image.
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
}

// Getter is used to get images but does not make changes
type Getter interface {
	Get(ctx context.Context, name string) (images.Image, error)
}

type imageReference struct {
	id       string
	target   imagespec.Descriptor
	platform string
}

// Store stores all images.
type Store struct {
	lock sync.RWMutex
	// refCache records the config ID and persisted identity of each reference.
	refCache map[string]imageReference

	// images is the local image store
	images Getter

	// content provider
	provider content.InfoReaderProvider

	// platform represents the currently supported platform for images
	// TODO: Make this store multi-platform
	platform platforms.MatchComparer

	// store is the legacy internal image store indexed by image id.
	store *store
	// runtimeStores partitions image IDs and pinned references by runtime handler.
	runtimeStores map[string]*store
}

// NewStore creates an image store.
func NewStore(img Getter, provider content.InfoReaderProvider, platform platforms.MatchComparer) *Store {
	return &Store{
		refCache:      make(map[string]imageReference),
		images:        img,
		provider:      provider,
		platform:      platform,
		store:         newStore(),
		runtimeStores: make(map[string]*store),
	}
}

func newStore() *store {
	return &store{
		images:     make(map[string]Image),
		digestSet:  digestset.NewSet(),
		pinnedRefs: make(map[string]sets.Set[string]),
	}
}

// runtimeStore requires the Store lock when the store is shared.
func (s *Store) runtimeStore(handler string) *store {
	if handler == "" {
		return s.store
	}
	if st := s.runtimeStores[handler]; st != nil {
		return st
	}
	st := newStore()
	s.runtimeStores[handler] = st
	return st
}

// Update updates cache for a reference.
func (s *Store) Update(ctx context.Context, ref string) error {
	return s.UpdateWithPlatform(ctx, ref, nil)
}

// UpdateWithPlatform updates cache for a reference using the given platform matcher.
// A nil matcher uses the persisted platform or the store default.
func (s *Store) UpdateWithPlatform(ctx context.Context, ref string, platform platforms.MatchComparer) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	if _, _, err := ParseName(ref); err != nil {
		return err
	}
	i, err := s.images.Get(ctx, ref)
	if err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("get image from containerd: %w", err)
	}

	var img *Image
	if err == nil {
		if i.Name != ref {
			return fmt.Errorf("image name %q does not match reference %q: %w", i.Name, ref, errdefs.ErrInvalidArgument)
		}
		img, err = s.getImage(ctx, i, platform)
		if err != nil {
			if cached, ok := s.refCache[ref]; errdefs.IsNotFound(err) && ok && (cached.target.Digest != i.Target.Digest || cached.target.MediaType != i.Target.MediaType ||
				cached.platform != i.Labels[labels.ImagePlatformLabelKey]) {
				if err := s.update(ref, nil); err != nil {
					return err
				}
			}
			return fmt.Errorf("get image info from containerd: %w", err)
		}
	}
	if err := s.update(ref, img); err != nil {
		return err
	}
	if img != nil {
		s.refCache[ref] = imageReference{id: img.ID, target: i.Target, platform: i.Labels[labels.ImagePlatformLabelKey]}
	}
	return nil
}

// update updates the internal cache. img == nil means that
// the image does not exist in containerd. The caller holds the Store lock.
func (s *Store) update(ref string, img *Image) error {
	_, handler, err := ParseName(ref)
	if err != nil {
		return err
	}
	if img != nil && img.RuntimeHandler != handler {
		return fmt.Errorf("image runtime handler does not match reference %q: %w", ref, errdefs.ErrInvalidArgument)
	}
	old, oldExist := s.refCache[ref]
	if img == nil && !oldExist {
		return nil
	}
	st := s.runtimeStore(handler)
	if img == nil {
		// Remove the reference only from its owning runtime store.
		st.delete(old.id, ref)
		delete(s.refCache, ref)
		return nil
	}
	if oldExist {
		if old.id == img.ID {
			if err := st.add(*img); err != nil {
				return err
			}
			if !img.Pinned {
				return st.unpin(img.ID, ref)
			}
			return nil
		}
		// Updated. Remove tag from old image.
		st.delete(old.id, ref)
	}
	// New image. Add new image.
	if err := st.add(*img); err != nil {
		return err
	}
	s.refCache[ref] = imageReference{id: img.ID}
	return nil
}

// getImage gets image information from containerd for the given platform matcher.
// A nil matcher uses the persisted platform or the store default.
func (s *Store) getImage(ctx context.Context, i images.Image, platform platforms.MatchComparer) (*Image, error) {
	_, handler, err := ParseName(i.Name)
	if err != nil {
		return nil, err
	}
	if platform == nil {
		platform = s.platform
		if value := i.Labels[labels.ImagePlatformLabelKey]; value != "" {
			selected, err := platforms.Parse(value)
			if err != nil {
				return nil, fmt.Errorf("get platform for image %q: %w", i.Name, err)
			}
			platform = platforms.Only(selected)
		}
	}
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

	return &Image{
		ID:             id,
		RuntimeHandler: handler,
		References:     []string{i.Name},
		ChainID:        chainID.String(),
		Size:           size,
		ImageSpec:      spec,
		Pinned:         pinned,
	}, nil

}

// Resolve resolves a image reference to image id.
func (s *Store) Resolve(ref string) (string, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	cached, ok := s.refCache[ref]
	if !ok {
		return "", errdefs.ErrNotFound
	}
	return cached.id, nil
}

// References returns the cached identities of an image's references.
func (s *Store) References(image Image) []images.Image {
	s.lock.RLock()
	defer s.lock.RUnlock()
	var references []images.Image
	for _, name := range image.References {
		if cached, ok := s.refCache[name]; ok && cached.id == image.ID {
			references = append(references, images.Image{Name: name, Target: cached.target,
				Labels: map[string]string{labels.ImagePlatformLabelKey: cached.platform}})
		}
	}
	return references
}

// ResolveForRuntime resolves an image reference only within the given handler.
func (s *Store) ResolveForRuntime(ref, handler string) (string, error) {
	decoded, owner, err := ParseName(ref)
	if err != nil {
		return "", err
	}
	if owner != "" && owner != handler {
		return "", errdefs.ErrNotFound
	}
	return s.Resolve(Name(decoded, handler))
}

// Get gets legacy image metadata by image id. The id can be truncated.
// Returns various validation errors if the image id is invalid.
// Returns errdefs.ErrNotFound if the image doesn't exist.
func (s *Store) Get(id string) (Image, error) {
	return s.store.get(id)
}

// GetForRuntime gets image metadata only within the given handler.
// The id can be truncated, but images from other handlers are never considered.
func (s *Store) GetForRuntime(id, handler string) (Image, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	if handler == "" {
		return s.store.get(id)
	}
	st := s.runtimeStores[handler]
	if st == nil {
		return Image{}, errdefs.ErrNotFound
	}
	return st.get(id)
}

// List lists all images, including separate entries for each runtime handler.
func (s *Store) List() []Image {
	s.lock.RLock()
	defer s.lock.RUnlock()
	images := s.store.list()
	for _, st := range s.runtimeStores {
		images = append(images, st.list()...)
	}
	return images
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
	// Refresh metadata while preserving references and pins from other names.
	references := util.MergeStringSlices(i.References, img.References)
	for n, name := range references {
		ref, _, err := ParseName(name)
		if err != nil {
			return err
		}
		references[n] = ref
	}
	img.References = docker.Sort(references)
	for n, ref := range img.References {
		img.References[n] = Name(ref, img.RuntimeHandler)
	}
	img.Pinned = i.Pinned || img.Pinned
	s.images[img.ID] = img
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
	i.References = slices.DeleteFunc(slices.Clone(i.References), func(name string) bool { return name == ref })
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
