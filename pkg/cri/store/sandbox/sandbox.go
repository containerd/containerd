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

package sandbox

import (
	"sync"

	"github.com/containerd/containerd/pkg/cri/store/label"
	"github.com/containerd/containerd/pkg/cri/store/truncindex"

	"github.com/containerd/containerd/pkg/cri/store"
)

// Sandbox interface stored by Store
type Sandbox interface {
	GetMetadata() *Metadata
	GetStatus() StatusStorage
	Stop()
	Stopped() <-chan struct{}
}

// Store stores all sandboxes.
type Store struct {
	lock      sync.RWMutex
	sandboxes map[string]Sandbox
	idIndex   *truncindex.TruncIndex
	labels    *label.Store
}

// NewStore creates a sandbox store.
func NewStore(labels *label.Store) *Store {
	return &Store{
		sandboxes: make(map[string]Sandbox),
		idIndex:   truncindex.NewTruncIndex([]string{}),
		labels:    labels,
	}
}

// Add a sandbox into the store.
func (s *Store) Add(sb Sandbox) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	if _, ok := s.sandboxes[sb.GetMetadata().ID]; ok {
		return store.ErrAlreadyExist
	}
	if err := s.labels.Reserve(sb.GetMetadata().ProcessLabel); err != nil {
		return err
	}
	if err := s.idIndex.Add(sb.GetMetadata().ID); err != nil {
		return err
	}
	s.sandboxes[sb.GetMetadata().ID] = sb
	return nil
}

// Get returns the sandbox with specified id.
// Returns store.ErrNotExist if the sandbox doesn't exist.
func (s *Store) Get(id string) (Sandbox, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	id, err := s.idIndex.Get(id)
	if err != nil {
		if err == truncindex.ErrNotExist {
			err = store.ErrNotExist
		}
		return nil, err
	}
	if sb, ok := s.sandboxes[id]; ok {
		return sb, nil
	}
	return nil, store.ErrNotExist
}

// List lists all sandboxes.
func (s *Store) List() []Sandbox {
	s.lock.RLock()
	defer s.lock.RUnlock()
	var sandboxes []Sandbox
	for _, sb := range s.sandboxes {
		sandboxes = append(sandboxes, sb)
	}
	return sandboxes
}

// Delete deletes the sandbox with specified id.
func (s *Store) Delete(id string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	id, err := s.idIndex.Get(id)
	if err != nil {
		// Note: The idIndex.Delete and delete doesn't handle truncated index.
		// So we need to return if there are error.
		return
	}
	s.labels.Release(s.sandboxes[id].GetMetadata().ProcessLabel)
	s.idIndex.Delete(id) // nolint: errcheck
	delete(s.sandboxes, id)
}
