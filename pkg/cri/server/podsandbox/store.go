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
	"sync"

	containerd "github.com/containerd/containerd/v2/client"
)

type Status struct {
	Waiter <-chan containerd.ExitStatus
}

type Store struct {
	sync.Map
}

func NewStore() *Store {
	return &Store{}
}

func (s *Store) Save(id string, exitCh <-chan containerd.ExitStatus) {
	s.Store(id, &Status{Waiter: exitCh})
}

func (s *Store) Get(id string) *Status {
	i, ok := s.LoadAndDelete(id)
	if !ok {
		// not exist
		return nil
	}
	// Only save *Status
	return i.(*Status)
}
