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

package net

import (
	"context"
	"strings"
	"testing"

	"github.com/containerd/containerd/gc"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/namespaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

func TestGC(t *testing.T) {
	ctx := leases.WithLease(namespaces.WithNamespace(context.Background(), TestNamespace), TestLease)
	store := testDBStore(t)
	api := &mockAPI{m: testManager(t, store)}
	collector := NewResourceCollector(api, store.db)
	assert.Equal(t, collector.ReferenceLabel(), "network")

	manager, err := api.NewManager(TestManager)
	require.NoError(t, err)

	nw, err := manager.Create(ctx, WithConflist(testNetworkConfList(t)))
	require.NoError(t, err)
	_, err = nw.Attach(ctx, WithContainer(TestContainer), WithNSPath(TestNSPath), WithIFName(TestInterface))
	require.NoError(t, err)

	gctx, err := collector.StartCollection(ctx)
	require.NoError(t, err)

	node := gcnode(metadata.ResourceNetwork, TestNamespace, strings.Join([]string{TestManager, TestAttachment}, "/"))
	found := func(n gc.Node) {
		assert.Equal(t, n, node)
	}
	notfound := func(n gc.Node) {
		assert.Fail(t, "not expected to be reached")
	}

	gctx.All(found)
	gctx.Active(TestNamespace, found)
	gctx.Active("non-existing-ns", notfound)
	gctx.Leased(TestNamespace, TestLease, found)
	gctx.Leased("non-existing-ns", TestLease, notfound)
	gctx.Leased(TestNamespace, "non-existing-lease", notfound)
	gctx.Remove(node)
	assert.NoError(t, gctx.Finish())

	_, err = manager.Attachment(ctx, TestAttachment)
	assert.Error(t, err)
}

func TestGCAfterRemove(t *testing.T) {
	ctx := leases.WithLease(namespaces.WithNamespace(context.Background(), TestNamespace), TestLease)
	store := testDBStore(t)
	api := &mockAPI{m: testManager(t, store)}
	collector := NewResourceCollector(api, store.db)
	assert.Equal(t, collector.ReferenceLabel(), "network")

	manager, err := api.NewManager(TestManager)
	require.NoError(t, err)

	nw, err := manager.Create(ctx, WithConflist(testNetworkConfList(t)))
	require.NoError(t, err)
	att, err := nw.Attach(ctx, WithContainer(TestContainer), WithNSPath(TestNSPath), WithIFName(TestInterface))
	require.NoError(t, err)
	require.NoError(t, att.Remove(ctx))

	gctx, err := collector.StartCollection(ctx)
	require.NoError(t, err)

	notfound := func(n gc.Node) {
		assert.Fail(t, "not expected to be reached")
	}

	gctx.All(notfound)
	gctx.Active(TestNamespace, notfound)
	gctx.Leased(TestNamespace, TestLease, notfound)
	assert.NoError(t, gctx.Finish())
}

func TestGCNamespaceCleanup(t *testing.T) {
	ctx := leases.WithLease(namespaces.WithNamespace(context.Background(), TestNamespace), TestLease)
	store := testDBStore(t)
	api := &mockAPI{m: testManager(t, store)}
	collector := NewResourceCollector(api, store.db)
	assert.Equal(t, collector.ReferenceLabel(), "network")

	manager, err := api.NewManager(TestManager)
	require.NoError(t, err)
	nw, err := manager.Create(ctx, WithConflist(testNetworkConfList(t)))
	require.NoError(t, err)
	att, err := nw.Attach(ctx, WithContainer(TestContainer), WithNSPath(TestNSPath), WithIFName(TestInterface))
	require.NoError(t, err)
	require.NoError(t, att.Remove(ctx))
	require.NoError(t, nw.Delete(ctx))

	gctx, err := collector.StartCollection(ctx)
	require.NoError(t, err)
	require.NoError(t, gctx.Finish())

	store.db.View(func(tx *bolt.Tx) error {
		assert.Nil(t, getBucket(tx, []byte(TestNamespace)))
		return nil
	})
}
