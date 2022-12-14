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
	"testing"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/namespaces"
	"github.com/containernetworking/cni/libcni"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNetworkFromRecord(t *testing.T) {
	rec := testNetworkRecord(t)
	n := networkFromRecord(rec, testCNI(t), testMemStore(t))
	assert.NotNil(t, n)
	assert.Equal(t, n.networkRecord, *rec)
}

func TestNetworkObject(t *testing.T) {
	obj := testNetwork(t)
	assert.Equal(t, obj.Name(), TestNetwork)
	assert.Equal(t, obj.Manager(), TestManager)
	assert.Equal(t, obj.Config(), testNetworkConfList(t))
}

func TestNetworkUpdate(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	n := testNetwork(t)
	c, ok := n.cni.(*mockCNI)
	require.True(t, ok)
	att, err := n.Attach(ctx, WithContainer(TestContainer), WithIFName(TestInterface))
	require.NoError(t, err)

	old := n.Config()
	updated, err := libcni.ConfListFromBytes([]byte(`{
		"cniVersion": "0.3.1",
		"name": "testnet",
		"plugins": [{
			"type": "bridge",
			"bridge": "cni0",
			"ipam": {
        "type": "host-local",
        "subnet": "10.10.0.0/16"
      }
		}]
		}`))
	require.NoError(t, err)

	// In case of network definition update, we expect existing
	// attachments to use the original network definitions
	// for check/delete operations
	assert.NoError(t, n.Update(ctx, WithConflist(updated)))
	assert.NoError(t, att.Remove(ctx))
	assert.Equal(t, old, c.cl)

	notfound, err := libcni.ConfListFromBytes([]byte(`{
		"cniVersion": "0.3.1",
		"name": "notfound",
		"plugins": [{
			"type": "bridge",
			"bridge": "cni0",
			"ipam": {
        "type": "host-local",
        "subnet": "10.10.0.0/16"
      }
		}]
		}`))
	require.NoError(t, err)
	assert.Error(t, n.Update(ctx, WithConflist(notfound)))
}

func TestNetworkDelete(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	n := testNetwork(t)

	c, ok := n.cni.(*mockCNI)
	require.True(t, ok)
	s, ok := n.store.(*mockStore)
	require.True(t, ok)
	s.nets[n.Name()] = &n.networkRecord

	att, err := n.Attach(ctx, WithContainer(TestContainer), WithIFName(TestInterface))
	require.NoError(t, err)
	assert.NoError(t, n.Delete(ctx))
	assert.NoError(t, att.Remove(ctx))
	assert.Equal(t, n.Config(), c.cl)
}

func TestNetworkAttachment(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	n := testNetwork(t)
	c, ok := n.cni.(*mockCNI)
	require.True(t, ok)
	s, ok := n.store.(*mockStore)
	require.True(t, ok)
	s.err, c.err = nil, nil

	_, err := n.Attach(ctx)
	assert.Error(t, err)
	_, err = n.Attach(ctx, WithContainer(TestContainer))
	assert.Error(t, err)
	_, err = n.Attach(ctx, WithContainer(TestContainer), WithNSPath(TestNSPath))
	assert.Error(t, err)
	att, err := n.Attach(ctx, WithContainer(TestContainer), WithIFName(TestInterface), WithNSPath(TestNSPath))
	assert.NoError(t, err)
	require.NoError(t, att.Remove(ctx))

	c.err = errdefs.ErrUnknown
	_, err = n.Attach(ctx, WithContainer(TestContainer), WithIFName(TestInterface), WithNSPath(TestNSPath))
	assert.Error(t, err)

	s.err, c.err = errdefs.ErrAlreadyExists, nil
	_, err = n.Attach(ctx, WithContainer(TestContainer), WithIFName(TestInterface), WithNSPath(TestNSPath))
	assert.Error(t, err)
}

func TestNetworkList(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	n := testNetwork(t)

	ls := n.List(ctx)
	assert.Empty(t, ls)

	att, err := n.Attach(ctx, WithContainer(TestContainer), WithIFName(TestInterface))
	require.NoError(t, err)
	ls = n.List(ctx)
	if assert.Equal(t, len(ls), 1) {
		assert.Equal(t, ls[0], att)
	}

	require.NoError(t, att.Remove(ctx))
	ls = n.List(ctx)
	assert.Empty(t, ls)
}

func testNetwork(t *testing.T) *network {
	n := networkFromRecord(testNetworkRecord(t), testCNI(t), testMemStore(t))
	require.NotNil(t, n)
	return n
}
