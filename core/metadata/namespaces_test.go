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

package metadata

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/protobuf/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
)

func TestCreateDelete(t *testing.T) {
	ctx, db := testDB(t)

	subtests := []struct {
		name     string
		create   func(t *testing.T, ctx context.Context)
		validate func(t *testing.T, err error)
	}{
		{
			name:   "empty",
			create: func(t *testing.T, ctx context.Context) {},
			validate: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "not-empty",
			create: func(t *testing.T, ctx context.Context) {
				store := NewContainerStore(db)
				_, err := store.Create(ctx, containers.Container{
					ID:      "c1",
					Runtime: containers.RuntimeInfo{Name: "rt"},
					Spec:    &types.Any{},
				})
				require.NoError(t, err)

				db.Update(func(tx *bbolt.Tx) error {
					ns, err := namespaces.NamespaceRequired(ctx)
					if err != nil {
						return err
					}
					bucket, err := createSnapshotterBucket(tx, ns, "testss")
					if err != nil {
						return err
					}
					return bucket.Put([]byte("key"), []byte("value"))
				})
			},
			validate: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), `still has containers, snapshots on "testss" snapshotter`)
			},
		},
	}

	for _, subtest := range subtests {
		ns := subtest.name
		ctx = namespaces.WithNamespace(ctx, ns)

		t.Run(subtest.name, func(t *testing.T) {
			err := db.Update(func(tx *bbolt.Tx) error {
				store := NewNamespaceStore(tx)
				return store.Create(ctx, ns, nil)
			})
			require.NoError(t, err)

			subtest.create(t, ctx)

			err = db.Update(func(tx *bbolt.Tx) error {
				store := NewNamespaceStore(tx)
				return store.Delete(ctx, ns)
			})
			subtest.validate(t, err)
		})
	}
}
