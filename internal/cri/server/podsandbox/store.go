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

package podsandbox

import (
	"fmt"
	"sync"

	"github.com/containerd/containerd/v2/internal/cri/server/podsandbox/types"
)

type Store struct {
	m sync.Map
}

func NewStore() *Store {
	return &Store{}
}

func (s *Store) Save(p *types.PodSandbox) error {
	if p == nil {
		return fmt.Errorf("pod sandbox should not be nil")
	}
	s.m.Store(p.ID, p)
	return nil
}

func (s *Store) Get(id string) *types.PodSandbox {
	i, ok := s.m.Load(id)
	if !ok {
		return nil
	}
	return i.(*types.PodSandbox)
}

func (s *Store) Remove(id string) *types.PodSandbox {
	i, ok := s.m.LoadAndDelete(id)
	if !ok {
		return nil
	}
	return i.(*types.PodSandbox)
}
