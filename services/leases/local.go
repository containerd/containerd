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
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"google.golang.org/grpc"

	"github.com/boltdb/bolt"
	api "github.com/containerd/containerd/api/services/leases/v1"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/services"
	ptypes "github.com/gogo/protobuf/types"
	"golang.org/x/net/context"
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

func (l *local) Create(ctx context.Context, r *api.CreateRequest, _ ...grpc.CallOption) (*api.CreateResponse, error) {
	lid := r.ID
	if lid == "" {
		lid = generateLeaseID()
	}
	var trans metadata.Lease
	if err := l.db.Update(func(tx *bolt.Tx) error {
		var err error
		trans, err = metadata.NewLeaseManager(tx).Create(ctx, lid, r.Labels)
		return err
	}); err != nil {
		return nil, err
	}
	return &api.CreateResponse{
		Lease: txToGRPC(trans),
	}, nil
}

func (l *local) Delete(ctx context.Context, r *api.DeleteRequest, _ ...grpc.CallOption) (*ptypes.Empty, error) {
	if err := l.db.Update(func(tx *bolt.Tx) error {
		return metadata.NewLeaseManager(tx).Delete(ctx, r.ID)
	}); err != nil {
		return nil, err
	}
	return &ptypes.Empty{}, nil
}

func (l *local) List(ctx context.Context, r *api.ListRequest, _ ...grpc.CallOption) (*api.ListResponse, error) {
	var leases []metadata.Lease
	if err := l.db.View(func(tx *bolt.Tx) error {
		var err error
		leases, err = metadata.NewLeaseManager(tx).List(ctx, false, r.Filters...)
		return err
	}); err != nil {
		return nil, err
	}

	apileases := make([]*api.Lease, len(leases))
	for i := range leases {
		apileases[i] = txToGRPC(leases[i])
	}

	return &api.ListResponse{
		Leases: apileases,
	}, nil
}

func txToGRPC(tx metadata.Lease) *api.Lease {
	return &api.Lease{
		ID:        tx.ID,
		Labels:    tx.Labels,
		CreatedAt: tx.CreatedAt,
		// TODO: Snapshots
		// TODO: Content
	}
}

func generateLeaseID() string {
	t := time.Now()
	var b [3]byte
	// Ignore read failures, just decreases uniqueness
	rand.Read(b[:])
	return fmt.Sprintf("%d-%s", t.Nanosecond(), base64.URLEncoding.EncodeToString(b[:]))
}
