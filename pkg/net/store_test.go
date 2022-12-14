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
	"bytes"
	"context"
	"net"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/namespaces"
	"github.com/containernetworking/cni/libcni"
	"github.com/containernetworking/cni/pkg/types"
	types100 "github.com/containernetworking/cni/pkg/types/100"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

const (
	TestNamespace  string = "testns"
	TestManager    string = "testmgr"
	TestNetwork    string = "testnet"
	TestLease      string = "testlease"
	TestContainer  string = "testcontainer"
	TestInterface  string = "eth0"
	TestNSPath     string = "/test/nspath"
	TestAttachment string = "testnet/testcontainer/eth0"
	TestGCLBKey    string = "containerd.io/gc.ref.network.testnet/eth0"
	TestGCLBVal    string = "testmgr/testnet/testcontainer/eth0"
)

func TestCreateNetwork(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	store := testDBStore(t)
	rec := testNetworkRecord(t)
	err := store.CreateNetwork(ctx, rec)
	assert.NoError(t, err)
	err = store.CreateNetwork(ctx, rec)
	assert.Error(t, err)
}

func TestUpdateNetwork(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	store := testDBStore(t)
	rec := testNetworkRecord(t)
	err := store.CreateNetwork(ctx, rec)
	require.NoError(t, err)

	err = store.UpdateNetwork(ctx, rec)
	assert.NoError(t, err)

	conflist, err := libcni.ConfListFromBytes([]byte(`{
		"cniVersion": "0.3.1",
		"name": "notfound",
		"plugins": [{
			"type": "bridge",
			"bridge": "cni0",
			"ipam": {
        "type": "host-local",
        "subnet": "10.1.0.0/16"
      }
		}]
		}`))
	require.NoError(t, err)
	notfound := &networkRecord{
		manager: TestManager,
		config:  conflist,
	}
	err = store.UpdateNetwork(ctx, notfound)
	assert.Error(t, err)
}

func TestGetNetwork(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	store := testDBStore(t)
	rec := testNetworkRecord(t)
	err := store.CreateNetwork(ctx, rec)
	require.NoError(t, err)

	rec2, err := store.GetNetwork(ctx, rec.manager, rec.config.Name)
	assert.NoError(t, err)
	assert.Equal(t, rec.manager, rec2.manager)
	assert.Equal(t, rec.config.Bytes, rec2.config.Bytes)

	rec3, err := store.GetNetwork(ctx, rec.manager, "notfound")
	assert.Error(t, err)
	assert.Nil(t, rec3)
}

func TestDeleteNetwork(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	store := testDBStore(t)
	rec := testNetworkRecord(t)
	err := store.CreateNetwork(ctx, rec)
	require.NoError(t, err)

	err = store.DeleteNetwork(ctx, rec.manager, rec.config.Name)
	assert.NoError(t, err)
	_, err = store.GetNetwork(ctx, rec.manager, rec.config.Name)
	assert.Error(t, err)
	err = store.DeleteNetwork(ctx, rec.manager, rec.config.Name)
	assert.Error(t, err)
}

func TestWalkNetworks(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	store := testDBStore(t)

	desc := []string{
		`{
			"cniVersion": "0.3.1",
			"name": "net1",
			"plugins": [{
				"type": "loopback"
			}]
		}`,
		`{
			"cniVersion": "0.3.1",
			"name": "net2",
			"plugins": [{
				"type": "loopback"
			}]
		}`,
	}

	nets := make(map[string]*libcni.NetworkConfigList)
	for _, d := range desc {
		c, err := libcni.ConfListFromBytes([]byte(d))
		require.NoError(t, err)
		nets[c.Name] = c
		nr := &networkRecord{
			manager: TestManager,
			config:  c,
		}
		err = store.CreateNetwork(ctx, nr)
		require.NoError(t, err)
	}

	nc := 0
	err := store.WalkNetworks(ctx, TestManager, func(nr *networkRecord) error {
		nc++
		ns, ok := nets[nr.config.Name]
		if assert.True(t, ok) {
			assert.Equal(t, ns.Bytes, nr.config.Bytes)
		}
		return nil
	})

	assert.Equal(t, nc, len(nets))
	assert.NoError(t, err)
}

func TestCreateAttachment(t *testing.T) {
	var err error
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	store := testDBStore(t)
	rec := testAttachmentRecord(t)
	deleted := false

	creators := []Creator{
		func(ctx context.Context) (*types100.Result, error) {
			return testAttachmentResult(t), nil
		},
		func(ctx context.Context) (*types100.Result, error) {
			return nil, errdefs.ErrAlreadyExists
		},
		func(ctx context.Context) (*types100.Result, error) {
			return nil, errdefs.ErrInvalidArgument
		},
	}

	deleter := func(ctx context.Context) error {
		deleted = true
		return nil
	}

	// creator failure
	err = store.CreateAttachment(ctx, rec, creators[1], deleter)
	assert.Error(t, err)
	assert.False(t, deleted)

	// creator failure
	err = store.CreateAttachment(ctx, rec, creators[2], deleter)
	assert.Error(t, err)
	assert.False(t, deleted)

	// lease already exists
	err = store.db.Update(func(tx *bolt.Tx) error {
		lbkt, e := createLeasesBucket(tx, TestNamespace, TestManager)
		require.NoError(t, e)
		e = writeLease(lbkt, TestLease, rec.id)
		require.NoError(t, e)
		return nil
	})
	require.NoError(t, err)
	err = store.CreateAttachment(ctx, rec, creators[0], deleter)
	assert.Error(t, err)
	assert.True(t, deleted)
	deleted = false

	// success
	err = store.db.Update(func(tx *bolt.Tx) error {
		lbkt := getLeasesBucket(tx, TestNamespace, TestManager)
		require.NotNil(t, lbkt)
		e := lbkt.DeleteBucket([]byte(TestLease))
		require.NoError(t, e)
		return nil
	})
	require.NoError(t, err)
	err = store.CreateAttachment(ctx, rec, creators[0], deleter)
	assert.NoError(t, err)
	assert.False(t, leaseExists(store.db, TestLease))

	// duplicate
	err = store.CreateAttachment(ctx, rec, creators[0], deleter)
	assert.Error(t, err)
}

func TestUpdateAttachment(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	store := testDBStore(t)
	rec := testAttachmentRecord(t)

	err := store.CreateAttachment(ctx, rec,
		func(ctx context.Context) (*types100.Result, error) {
			return testAttachmentResult(t), nil
		},
		func(ctx context.Context) error {
			return nil
		},
	)
	require.NoError(t, err)

	updater := func(ctx context.Context) error {
		return nil
	}

	err = store.UpdateAttachment(ctx, rec, updater)
	assert.NoError(t, err)

	rec.id = "not-found"
	err = store.UpdateAttachment(ctx, rec, updater)
	assert.Error(t, err)
}

func TestGetAttachment(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	store := testDBStore(t)
	rec := testAttachmentRecord(t)

	err := store.CreateAttachment(ctx, rec,
		func(ctx context.Context) (*types100.Result, error) {
			return testAttachmentResult(t), nil
		},
		func(ctx context.Context) error {
			return nil
		},
	)
	require.NoError(t, err)

	arec, err := store.GetAttachment(ctx, rec.manager, rec.id)
	assert.NoError(t, err)

	res1, res2 := rec.result, arec.result
	rec.result, arec.result = nil, nil
	assert.Equal(t, rec, arec)
	assert.True(t, attachmentResultEqual(res1, res2))

	_, err = store.GetAttachment(ctx, rec.manager, "not-found")
	assert.Error(t, err)
}

func TestDeleteAttachment(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	store := testDBStore(t)
	rec := testAttachmentRecord(t)

	creator := func(ctx context.Context) (*types100.Result, error) {
		return testAttachmentResult(t), nil
	}
	deleters := []Deleter{
		func(ctx context.Context) error {
			return nil
		},
		func(ctx context.Context) error {
			return errdefs.ErrInvalidArgument
		},
	}

	err := store.CreateAttachment(ctx, rec, creator, deleters[0])
	require.NoError(t, err)
	err = store.DeleteAttachment(ctx, rec.manager, rec.id, deleters[1])
	assert.Error(t, err)
	_, err = store.GetAttachment(ctx, rec.manager, rec.id)
	assert.NoError(t, err)
	err = store.DeleteAttachment(ctx, rec.manager, rec.id, deleters[0])
	assert.NoError(t, err)
	_, err = store.GetAttachment(ctx, rec.manager, rec.id)
	assert.Error(t, err)
	assert.False(t, leaseExists(store.db, TestLease))
}

func TestWalkAttachments(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	store := testDBStore(t)
	rec := testAttachmentRecord(t)

	err := store.CreateAttachment(ctx, rec,
		func(ctx context.Context) (*types100.Result, error) {
			return testAttachmentResult(t), nil
		},
		func(ctx context.Context) error {
			return nil
		},
	)
	require.NoError(t, err)

	cnt := 0
	res1 := rec.result
	rec.result = nil
	err = store.WalkAttachments(ctx, TestManager, rec.network.Name,
		func(att *attachmentRecord) error {
			cnt++
			res2 := att.result
			att.result = nil
			assert.Equal(t, rec, att)
			assert.True(t, attachmentResultEqual(res1, res2))
			return nil
		},
	)

	assert.NoError(t, err)
	assert.Equal(t, cnt, 1)
}

func testDB(t *testing.T) DB {
	db, err := bolt.Open(filepath.Join(t.TempDir(), "testnet.db"), 0644, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, db.Close())
	})
	return db
}

func testDBStore(t *testing.T) *store {
	s, err := NewStore(testDB(t))
	require.NoError(t, err)
	r, ok := s.(*store)
	require.True(t, ok)
	return r
}

func testNetworkConfList(t *testing.T) *libcni.NetworkConfigList {
	conflist, err := libcni.ConfListFromBytes([]byte(`{
		"cniVersion": "0.3.1",
		"name": "testnet",
		"plugins": [{
			"type": "bridge",
			"bridge": "cni0",
			"ipam": {
        "type": "host-local",
        "subnet": "10.1.0.0/16"
      }
		}]
		}`))
	require.NoError(t, err)
	return conflist
}

func testNetworkRecord(t *testing.T) *networkRecord {
	return &networkRecord{
		manager: TestManager,
		config:  testNetworkConfList(t),
	}
}

func testAttachmentRecord(t *testing.T) *attachmentRecord {
	return &attachmentRecord{
		id:      TestAttachment,
		manager: TestManager,
		network: testNetworkConfList(t),
		labels: map[string]string{
			"lease": TestLease,
		},
		args: AttachmentArgs{
			ContainerID: TestContainer,
			NSPath:      TestNSPath,
			IFName:      TestInterface,
			CapabilityArgs: map[string]interface{}{
				"ca": "ca value",
			},
			PluginArgs: map[string]string{
				"pa": "pa value",
			},
		},
	}
}

func testAttachmentResult(t *testing.T) *types100.Result {
	zero := 0
	ip, ipnet, err := net.ParseCIDR("127.0.0.1/24")
	require.NoError(t, err)
	return &types100.Result{
		CNIVersion: "1.0.0",
		Interfaces: []*types100.Interface{{Name: "lo", Mac: "mac", Sandbox: "sandbox"}},
		IPs:        []*types100.IPConfig{{Interface: &zero, Address: *ipnet, Gateway: ip}},
		Routes:     []*types.Route{{Dst: *ipnet, GW: ip}},
		DNS:        types.DNS{Nameservers: []string{"ns1"}, Domain: "abc.com", Search: []string{"search1"}, Options: []string{"option1"}},
	}
}

func attachmentResultEqual(r1, r2 *types100.Result) bool {
	var buf1, buf2 bytes.Buffer
	r1.PrintTo(&buf1)
	r2.PrintTo(&buf2)
	return bytes.Equal(buf1.Bytes(), buf2.Bytes())
}

func leaseExists(db DB, lease string) bool {
	res := false
	db.View(func(tx *bolt.Tx) error {
		bkt := getLeasesBucket(tx, TestNamespace, TestManager)
		if bkt != nil {
			if bkt.Get([]byte(lease)) != nil {
				res = true
			}
		}
		return nil
	})
	return res
}
