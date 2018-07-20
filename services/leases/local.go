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

package leases

import (
	"context"

	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/services"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.ServicePlugin,
		ID:   services.LeasesService,
		Requires: []plugin.Type{
			plugin.MetadataPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			m, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}
			return &local{db: m.(*metadata.DB)}, nil
		},
	})
}

type local struct {
	db *metadata.DB
}

func (l *local) Create(ctx context.Context, opts ...leases.Opt) (leases.Lease, error) {
	var lease leases.Lease
	if err := l.db.Update(func(tx *bolt.Tx) error {
		var err error
		lease, err = metadata.NewLeaseManager(tx).Create(ctx, opts...)
		return err
	}); err != nil {
		return leases.Lease{}, err
	}
	return lease, nil
}

func (l *local) Delete(ctx context.Context, lease leases.Lease) error {
	return l.db.Update(func(tx *bolt.Tx) error {
		return metadata.NewLeaseManager(tx).Delete(ctx, lease)
	})
}

func (l *local) List(ctx context.Context, filters ...string) ([]leases.Lease, error) {
	var ll []leases.Lease
	if err := l.db.View(func(tx *bolt.Tx) error {
		var err error
		ll, err = metadata.NewLeaseManager(tx).List(ctx, filters...)
		return err
	}); err != nil {
		return nil, err
	}
	return ll, nil
}
