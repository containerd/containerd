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
	"fmt"

	"github.com/containerd/platforms"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
)

// NewFakeStore returns an image store with predefined images.
// Update is not allowed for this fake store.
func NewFakeStore(images []Image) (*Store, error) {
	s := NewStore(nil, nil, "", nil)
	if s.platforms == nil {
		s.platforms = make(map[string]imagespec.Platform)
		s.platforms[s.defaultRuntimeName] = platforms.DefaultSpec()
	}
	for _, i := range images {
		for _, ref := range i.References {
			refKey := RefKey{Ref: ref, RuntimeHandler: i.Key.RuntimeHandler}
			imageIDKey := ImageIDKey{ID: i.Key.ID, RuntimeHandler: i.Key.RuntimeHandler}
			s.refCache[refKey] = imageIDKey
		}
		if err := s.store.add(i); err != nil {
			return nil, fmt.Errorf("add image %+v: %w", i, err)
		}
	}
	return s, nil
}
