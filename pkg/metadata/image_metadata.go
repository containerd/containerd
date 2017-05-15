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

package metadata

import (
	"encoding/json"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata/store"
)

// The code is very similar to sandbox.go, but there is no template support
// in golang, thus similar files for different types.
// TODO(random-liu): Figure out a way to simplify this.
// TODO(random-liu): Handle versioning

// imageMetadataVersion is current version of image metadata.
const imageMetadataVersion = "v1" // nolint

// versionedImageMetadata is the internal struct representing the versioned
// image metadata
// nolint
type versionedImageMetadata struct {
	// Version indicates the version of the versioned image metadata.
	Version string `json:"version,omitempty"`
	ImageMetadata
}

// ImageMetadata is the unversioned image metadata.
type ImageMetadata struct {
	// Id of the image. Normally the Digest
	ID string `json:"id,omitempty"`
	// Other names by which this image is known.
	RepoTags []string `json:"repo_tags,omitempty"`
	// Digests by which this image is known.
	RepoDigests []string `json:"repo_digests,omitempty"`
	// Size of the image in bytes. Must be > 0.
	Size uint64 `json:"size,omitempty"`
}

// ImageMetadataUpdateFunc is the function used to update ImageMetadata.
type ImageMetadataUpdateFunc func(ImageMetadata) (ImageMetadata, error)

// imageMetadataToStoreUpdateFunc generates a metadata store UpdateFunc from ImageMetadataUpdateFunc.
func imageMetadataToStoreUpdateFunc(u ImageMetadataUpdateFunc) store.UpdateFunc {
	return func(data []byte) ([]byte, error) {
		meta := &ImageMetadata{}
		if err := json.Unmarshal(data, meta); err != nil {
			return nil, err
		}
		newMeta, err := u(*meta)
		if err != nil {
			return nil, err
		}
		return json.Marshal(newMeta)
	}
}

// ImageMetadataStore is the store for metadata of all images.
type ImageMetadataStore interface {
	// Create creates an image's metadata from ImageMetadata in the store.
	Create(ImageMetadata) error
	// Get gets the specified image metadata.
	Get(string) (*ImageMetadata, error)
	// Update updates a specified image metatdata.
	Update(string, ImageMetadataUpdateFunc) error
	// List lists all image metadatas.
	List() ([]*ImageMetadata, error)
	// Delete deletes the image's metatdata from the store.
	Delete(string) error
}

// imageMetadataStore is an implmentation of ImageMetadataStore.
type imageMetadataStore struct {
	store store.MetadataStore
}

// NewImageMetadataStore creates an ImageMetadataStore from a basic MetadataStore.
func NewImageMetadataStore(store store.MetadataStore) ImageMetadataStore {
	return &imageMetadataStore{store: store}
}

// Create creates a image's metadata from ImageMetadata in the store.
func (s *imageMetadataStore) Create(metadata ImageMetadata) error {
	data, err := json.Marshal(&metadata)
	if err != nil {
		return err
	}
	return s.store.Create(metadata.ID, data)
}

// Get gets the specified image metadata.
func (s *imageMetadataStore) Get(digest string) (*ImageMetadata, error) {
	data, err := s.store.Get(digest)
	if err != nil {
		return nil, err
	}
	imageMetadata := &ImageMetadata{}
	if err := json.Unmarshal(data, imageMetadata); err != nil {
		return nil, err
	}
	return imageMetadata, nil
}

// Update updates a specified image's metadata. The function is running in a
// transaction. Update will not be applied when the update function
// returns error.
func (s *imageMetadataStore) Update(digest string, u ImageMetadataUpdateFunc) error {
	return s.store.Update(digest, imageMetadataToStoreUpdateFunc(u))
}

// List lists all image metadata.
func (s *imageMetadataStore) List() ([]*ImageMetadata, error) {
	allData, err := s.store.List()
	if err != nil {
		return nil, err
	}
	var imageMetadataA []*ImageMetadata
	for _, data := range allData {
		imageMetadata := &ImageMetadata{}
		if err := json.Unmarshal(data, imageMetadata); err != nil {
			return nil, err
		}
		imageMetadataA = append(imageMetadataA, imageMetadata)
	}
	return imageMetadataA, nil
}

// Delete deletes the image metadata from the store.
func (s *imageMetadataStore) Delete(digest string) error {
	return s.store.Delete(digest)
}
