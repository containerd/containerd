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

package store

import (
	"errors"
	"sync"

	"github.com/golang/glog"
)

var (
	// ErrNotExist is the error returned when specified id does
	// not exist.
	ErrNotExist = errors.New("does not exist")
	// ErrAlreadyExist is the error returned when specified id already
	// exists.
	ErrAlreadyExist = errors.New("already exists")
)

// All byte arrays are expected to be read-only. User MUST NOT modify byte
// array element directly!!

// UpdateFunc is function used to update a specific metadata. The value
// passed in is the old value, it MUST NOT be changed in the function.
// The function should make a copy of the old value and apply update on
// the copy. The updated value should be returned. If there is an error,
// the update will be rolled back.
type UpdateFunc func([]byte) ([]byte, error)

// MetadataStore is the interface for storing metadata. All methods should
// be thread-safe.
// TODO(random-liu): Initialize the metadata store with a type, and replace
// []byte with interface{}, so as to avoid extra marshal/unmarshal on the
// user side.
type MetadataStore interface {
	// Create the metadata containing the passed in data with the
	// specified id.
	// Note:
	// * Create MUST return error if the id already exists.
	// * The id and data MUST be added in one transaction to the store.
	Create(string, []byte) error
	// Get the data by id.
	// Note that Get MUST return ErrNotExist if the id doesn't exist.
	Get(string) ([]byte, error)
	// Update the data by id.
	// Note:
	// * Update MUST return ErrNotExist is the id doesn't exist.
	// * The update MUST be applied in one transaction.
	Update(string, UpdateFunc) error
	// List returns entire array of data from the store.
	List() ([][]byte, error)
	// Delete the data by id.
	// Note:
	// * Delete should be idempotent, it MUST not return error if the id
	// doesn't exist or has been removed.
	// * The id and data MUST be deleted in one transaction.
	Delete(string) error
}

// TODO(random-liu) Add checkpoint. When checkpoint is enabled, it should cache data
// in memory and checkpoint metadata into files during update. Metadata should serve
// from memory, but any modification should be checkpointed, so that memory could be
// recovered after restart. It should be possible to disable the checkpoint for testing.
// Note that checkpoint update may fail, so the recovery logic should tolerate that.

// metadata is the internal type for storing data in metadataStore.
type metadata struct {
	sync.RWMutex
	data []byte
}

// newMetadata creates a new metadata.
func newMetadata(data []byte) (*metadata, error) {
	return &metadata{data: data}, nil
	// TODO(random-liu): Create the data on disk atomically.
}

// get a snapshot of the metadata.
func (m *metadata) get() []byte {
	m.RLock()
	defer m.RUnlock()
	return m.data
}

// update the value.
func (m *metadata) update(u UpdateFunc) error {
	m.Lock()
	defer m.Unlock()
	newData, err := u(m.data)
	if err != nil {
		return err
	}
	// Replace with newData, user holding the old data will not
	// be affected.
	// TODO(random-liu) *Update* existing data on disk atomically,
	// return error if checkpoint failed.
	m.data = newData
	return nil
}

// delete deletes the data on disk atomically.
func (m *metadata) delete() error {
	// TODO(random-liu): Hold write lock, rename the data on the disk.
	return nil
}

// cleanup cleans up all temporary files left-over.
func (m *metadata) cleanup() error {
	// TODO(random-liu): Hold write lock, Cleanup temporary files generated
	// in atomic file operations. The write lock makes sure there is no on-going
	// update, so any temporary files could be removed.
	return nil
}

// metadataStore is metadataStore is an implementation of MetadataStore.
type metadataStore struct {
	sync.RWMutex
	metas map[string]*metadata
}

// NewMetadataStore creates a MetadataStore.
func NewMetadataStore() MetadataStore {
	// TODO(random-liu): Recover state from disk checkpoint.
	// TODO(random-liu): Cleanup temporary files left over.
	return &metadataStore{metas: map[string]*metadata{}}
}

// createMetadata creates metadata with a read-write lock
func (m *metadataStore) createMetadata(id string, meta *metadata) error {
	m.Lock()
	defer m.Unlock()
	if _, found := m.metas[id]; found {
		return ErrAlreadyExist
	}
	m.metas[id] = meta
	return nil
}

// Create the metadata with a specific id.
func (m *metadataStore) Create(id string, data []byte) (retErr error) {
	// newMetadata takes time, we may not want to lock around it.
	meta, err := newMetadata(data)
	if err != nil {
		return err
	}
	defer func() {
		// This should not happen, because if id already exists,
		// newMetadata should fail to checkpoint. Add this just
		// in case.
		if retErr != nil {
			meta.delete()  // nolint: errcheck
			meta.cleanup() // nolint: errcheck
		}
	}()
	return m.createMetadata(id, meta)
}

// getMetadata gets metadata by id with a read lock.
func (m *metadataStore) getMetadata(id string) (*metadata, bool) {
	m.RLock()
	defer m.RUnlock()
	meta, found := m.metas[id]
	return meta, found
}

// Get data by id.
func (m *metadataStore) Get(id string) ([]byte, error) {
	meta, found := m.getMetadata(id)
	if !found {
		return nil, ErrNotExist
	}
	return meta.get(), nil
}

// Update data by id.
func (m *metadataStore) Update(id string, u UpdateFunc) error {
	meta, found := m.getMetadata(id)
	if !found {
		return ErrNotExist
	}
	return meta.update(u)
}

// listMetadata lists all metadata with a read lock.
func (m *metadataStore) listMetadata() []*metadata {
	m.RLock()
	defer m.RUnlock()
	var metas []*metadata
	for _, meta := range m.metas {
		metas = append(metas, meta)
	}
	return metas
}

// List all data.
func (m *metadataStore) List() ([][]byte, error) {
	metas := m.listMetadata()
	var data [][]byte
	for _, meta := range metas {
		data = append(data, meta.get())
	}
	return data, nil
}

// Delete the data by id.
func (m *metadataStore) Delete(id string) error {
	meta, err := func() (*metadata, error) {
		m.Lock()
		defer m.Unlock()
		meta := m.metas[id]
		if meta == nil {
			return nil, nil
		}
		if err := meta.delete(); err != nil {
			return nil, err
		}
		delete(m.metas, id)
		return meta, nil
	}()
	if err != nil {
		return err
	}
	// The metadata is removed from the store at this point.
	if meta != nil {
		// Do not return error for cleanup.
		if err := meta.cleanup(); err != nil {
			glog.Errorf("Failed to cleanup metadata %q: %v", id, err)
		}
	}
	return nil
}
