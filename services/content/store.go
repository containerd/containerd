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

package content

import (
	"context"

	eventstypes "github.com/containerd/containerd/v2/api/events"
	"github.com/containerd/containerd/v2/content"
	"github.com/containerd/containerd/v2/events"
	"github.com/containerd/containerd/v2/metadata"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/services"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	digest "github.com/opencontainers/go-digest"
)

// store wraps content.Store with proper event published.
type store struct {
	content.Store
	publisher events.Publisher
}

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.ServicePlugin,
		ID:   services.ContentService,
		Requires: []plugin.Type{
			plugins.EventPlugin,
			plugins.MetadataPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			m, err := ic.GetSingle(plugins.MetadataPlugin)
			if err != nil {
				return nil, err
			}
			ep, err := ic.GetSingle(plugins.EventPlugin)
			if err != nil {
				return nil, err
			}

			s, err := newContentStore(m.(*metadata.DB).ContentStore(), ep.(events.Publisher))
			return s, err
		},
	})
}

func newContentStore(cs content.Store, publisher events.Publisher) (content.Store, error) {
	return &store{
		Store:     cs,
		publisher: publisher,
	}, nil
}

func (s *store) Delete(ctx context.Context, dgst digest.Digest) error {
	if err := s.Store.Delete(ctx, dgst); err != nil {
		return err
	}
	// TODO: Consider whether we should return error here.
	return s.publisher.Publish(ctx, "/content/delete", &eventstypes.ContentDelete{
		Digest: dgst.String(),
	})
}
