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
	"github.com/containernetworking/cni/pkg/types"
	types100 "github.com/containernetworking/cni/pkg/types/100"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagerObject(t *testing.T) {
	m := testManager(t, testMemStore(t))
	assert.Equal(t, m.Name(), TestManager)
}

func TestManagerCreate(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	s := testMemStore(t)
	m := testManager(t, s)
	n, err := m.Create(ctx, WithConflist(testNetworkConfList(t)))
	assert.NoError(t, err)
	assert.Equal(t, len(s.nets), 1)
	_, err = m.Create(ctx, WithConflist(testNetworkConfList(t)))
	assert.Error(t, err)
	require.NoError(t, n.Delete(ctx))
	assert.Empty(t, s.nets)
}

func TestManagerNetwork(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	s := testMemStore(t)
	m := testManager(t, s)
	_, err := m.Network(ctx, TestNetwork)
	assert.Error(t, err)

	net := testNetwork(t)
	s.nets[TestNetwork] = &net.networkRecord
	nr, err := m.Network(ctx, TestNetwork)
	if assert.NoError(t, err) {
		assert.Equal(t, net.networkRecord, nr.(*network).networkRecord)
	}
	delete(s.nets, TestNetwork)
	_, err = m.Network(ctx, TestNetwork)
	assert.Error(t, err)
}

func TestManagerList(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	s := testMemStore(t)
	m := testManager(t, s)
	_, err := m.Network(ctx, TestNetwork)
	assert.Error(t, err)
	n1, err := m.Create(ctx, WithConflist(testNetworkConfList(t)))
	require.NoError(t, err)
	ls := m.List(ctx)
	assert.Equal(t, len(ls), 1)
	for _, n := range ls {
		assert.Equal(t, n1, n)
	}
	assert.NoError(t, n1.Delete(ctx))
	ls = m.List(ctx)
	assert.Empty(t, ls)
}

func TestManagerAttachment(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), TestNamespace)
	s := testMemStore(t)
	m := testManager(t, s)

	att := testAttachment(t)
	s.atts[TestAttachment] = &att.attachmentRecord
	s.nets[TestNetwork] = createNetworkRecord(att.manager, att.network)

	ar, err := m.Attachment(ctx, att.ID())
	if assert.NoError(t, err) {
		assert.Equal(t, att.attachmentRecord, ar.(*attachment).attachmentRecord)
		assert.Equal(t, att.network, ar.(*attachment).network)
	}

	delete(s.nets, att.Network())
	// we expect to use the cached network config associated with the
	// attachment even if the original network definition is deleted
	_, err = m.Attachment(ctx, TestAttachment)
	assert.NoError(t, err)

	delete(s.atts, TestAttachment)
	_, err = m.Attachment(ctx, TestAttachment)
	assert.Error(t, err)
}

type mockAPI struct {
	m *manager
}

type mockCNI struct {
	err error
	res *types100.Result
	cl  *libcni.NetworkConfigList
	rt  *libcni.RuntimeConf
}

type mockStore struct {
	err  error
	nets map[string]*networkRecord
	atts map[string]*attachmentRecord
}

var (
	_ API        = (*mockAPI)(nil)
	_ libcni.CNI = (*mockCNI)(nil)
	_ Store      = (*mockStore)(nil)
)

func (api *mockAPI) NewManager(name string, opts ...ManagerOpt) (Manager, error) {
	return api.m, nil
}

func (api *mockAPI) Manager(name string) Manager {
	return api.m
}

func (c *mockCNI) AddNetworkList(ctx context.Context, net *libcni.NetworkConfigList, rt *libcni.RuntimeConf) (types.Result, error) {
	if c.err != nil {
		return nil, c.err
	}
	c.cl, c.rt = net, rt
	return c.res, nil
}

func (c *mockCNI) CheckNetworkList(ctx context.Context, net *libcni.NetworkConfigList, rt *libcni.RuntimeConf) error {
	c.cl, c.rt = net, rt
	return c.err
}

func (c *mockCNI) DelNetworkList(ctx context.Context, net *libcni.NetworkConfigList, rt *libcni.RuntimeConf) error {
	c.cl, c.rt = net, rt
	return c.err
}

func (c *mockCNI) GetNetworkListCachedResult(net *libcni.NetworkConfigList, rt *libcni.RuntimeConf) (types.Result, error) {
	return nil, errdefs.ErrNotImplemented
}

func (c *mockCNI) GetNetworkListCachedConfig(net *libcni.NetworkConfigList, rt *libcni.RuntimeConf) ([]byte, *libcni.RuntimeConf, error) {
	return nil, nil, errdefs.ErrNotImplemented
}

func (c *mockCNI) AddNetwork(ctx context.Context, net *libcni.NetworkConfig, rt *libcni.RuntimeConf) (types.Result, error) {
	return nil, errdefs.ErrNotImplemented
}

func (c *mockCNI) CheckNetwork(ctx context.Context, net *libcni.NetworkConfig, rt *libcni.RuntimeConf) error {
	return errdefs.ErrNotImplemented
}

func (c *mockCNI) DelNetwork(ctx context.Context, net *libcni.NetworkConfig, rt *libcni.RuntimeConf) error {
	return errdefs.ErrNotImplemented
}

func (c *mockCNI) GetNetworkCachedResult(net *libcni.NetworkConfig, rt *libcni.RuntimeConf) (types.Result, error) {
	return nil, errdefs.ErrNotImplemented
}

func (c *mockCNI) GetNetworkCachedConfig(net *libcni.NetworkConfig, rt *libcni.RuntimeConf) ([]byte, *libcni.RuntimeConf, error) {
	return nil, nil, errdefs.ErrNotImplemented
}

func (c *mockCNI) ValidateNetworkList(ctx context.Context, net *libcni.NetworkConfigList) ([]string, error) {
	return nil, errdefs.ErrNotImplemented
}

func (c *mockCNI) ValidateNetwork(ctx context.Context, net *libcni.NetworkConfig) ([]string, error) {
	return nil, errdefs.ErrNotImplemented
}

func (s *mockStore) CreateNetwork(ctx context.Context, r *networkRecord) error {
	if _, ok := s.nets[r.config.Name]; ok {
		return errdefs.ErrAlreadyExists
	}
	if s.err != nil {
		return s.err
	}
	s.nets[r.config.Name] = r
	return nil
}

func (s *mockStore) UpdateNetwork(ctx context.Context, r *networkRecord) error {
	if s.err != nil {
		return s.err
	}
	s.nets[r.config.Name] = r
	return nil
}

func (s *mockStore) GetNetwork(ctx context.Context, manager, id string) (*networkRecord, error) {
	if s.err != nil {
		return nil, s.err
	}
	r, ok := s.nets[id]
	if !ok {
		return nil, errdefs.ErrNotFound
	}
	return r, nil
}

func (s *mockStore) DeleteNetwork(ctx context.Context, manager, id string) error {
	_, err := s.GetNetwork(ctx, manager, id)
	if err != nil {
		return err
	}
	delete(s.nets, id)
	return nil
}

func (s *mockStore) WalkNetworks(ctx context.Context, manager string, fn func(*networkRecord) error) error {
	for _, v := range s.nets {
		fn(v)
	}
	return nil
}

func (s *mockStore) CreateAttachment(ctx context.Context, r *attachmentRecord, creator Creator, deleter Deleter) error {
	if _, ok := s.atts[r.id]; ok {
		return errdefs.ErrAlreadyExists
	}
	res, err := creator(ctx)
	if err != nil {
		return err
	}
	r.result = res
	if s.err != nil {
		deleter(ctx)
		return s.err
	}
	s.atts[r.id] = r
	return nil
}

func (s *mockStore) UpdateAttachment(ctx context.Context, r *attachmentRecord, updater Updater) error {
	return errdefs.ErrNotImplemented
}

func (s *mockStore) GetAttachment(ctx context.Context, manager, id string) (*attachmentRecord, error) {
	if s.err != nil {
		return nil, s.err
	}
	att, ok := s.atts[id]
	if !ok {
		return nil, errdefs.ErrNotFound
	}
	return att, nil
}

func (s *mockStore) DeleteAttachment(ctx context.Context, manager, id string, deleter Deleter) error {
	_, err := s.GetAttachment(ctx, manager, id)
	if err != nil {
		return err
	}
	if err := deleter(ctx); err != nil {
		return err
	}
	delete(s.atts, id)
	return nil
}

func (s *mockStore) WalkAttachments(ctx context.Context, manager, network string, fn func(*attachmentRecord) error) error {
	for _, v := range s.atts {
		fn(v)
	}
	return nil
}

func testManager(t *testing.T, store Store) *manager {
	return &manager{
		name:  TestManager,
		cni:   testCNI(t),
		store: store,
	}
}

func testCNI(t *testing.T) libcni.CNI {
	return &mockCNI{res: testAttachmentResult(t)}
}

func testMemStore(t *testing.T) *mockStore {
	return &mockStore{
		nets: make(map[string]*networkRecord),
		atts: make(map[string]*attachmentRecord),
	}
}
