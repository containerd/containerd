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
	"context"

	"github.com/pkg/errors"

	evt "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/sandbox"
	"github.com/containerd/containerd/services"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.ServicePlugin,
		ID:   services.SandboxService,
		Requires: []plugin.Type{
			plugin.MetadataPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			m, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}

			db := m.(*metadata.DB)
			sb := make(map[string]sandbox.Sandboxer)
			for n, srv := range db.Sandboxes() {
				sb[n] = newLocal(srv, ic.Events)
			}
			return sb, nil
		},
	})
}

type local struct {
	sandbox.Sandboxer
	publisher events.Publisher
}

var _ sandbox.Sandboxer = &local{}

func newLocal(srv sandbox.Sandboxer, publisher events.Publisher) *local {
	return &local{
		Sandboxer: srv,
		publisher: publisher,
	}
}

func (l *local) Start(ctx context.Context, instance *sandbox.Sandbox) (*sandbox.Sandbox, error) {
	out, err := l.Sandboxer.Create(ctx, instance)
	if err != nil {
		return nil, err
	}

	if err := l.publisher.Publish(ctx, "/sandbox/start", &evt.SandboxStart{
		ID: out.ID,
	}); err != nil {
		return nil, errors.Wrap(err, "failed to publish sandbox start event")
	}

	return out, nil
}

func (l *local) Stop(ctx context.Context, id string) error {
	if err := l.Sandboxer.Delete(ctx, id); err != nil {
		return err
	}

	if err := l.publisher.Publish(ctx, "/sandbox/stop", &evt.SandboxStart{
		ID: id,
	}); err != nil {
		return errors.Wrap(err, "failed to publish sandbox stop event")
	}

	return nil
}
