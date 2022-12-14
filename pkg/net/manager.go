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
	"os"

	"github.com/containerd/containerd/log"
	"github.com/containernetworking/cni/libcni"
	"github.com/containernetworking/cni/pkg/invoke"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/sirupsen/logrus"
)

const (
	DefaultCNIDir = "/opt/cni/bin"
)

type manager struct {
	name  string
	cni   libcni.CNI
	store Store
}

var _ Manager = (*manager)(nil)

func NewManager(name string, store Store, opts ...ManagerOpt) (Manager, error) {
	cfg := ManagerConfig{
		PluginDirs: []string{DefaultCNIDir},
	}
	for _, o := range opts {
		if err := o(&cfg); err != nil {
			return nil, err
		}
	}

	return &manager{
		name:  name,
		store: store,
		cni: libcni.NewCNIConfig(
			cfg.PluginDirs,
			&invoke.DefaultExec{
				RawExec:       &invoke.RawExec{Stderr: os.Stderr},
				PluginDecoder: version.PluginDecoder{},
			},
		),
	}, nil
}

func (m *manager) Name() string {
	return m.name
}

func (m *manager) Create(ctx context.Context, opts ...NetworkOpt) (Network, error) {
	c := NetworkConfig{}

	for _, o := range opts {
		if err := o(&c); err != nil {
			return nil, err
		}
	}

	if err := c.validate(); err != nil {
		return nil, err
	}

	log.G(ctx).WithFields(logrus.Fields{"manager": m.name, "network": c.Conflist.Name}).Debugf("create network")

	r := &networkRecord{
		manager: m.name,
		config:  c.Conflist,
		labels:  c.Labels,
	}

	if err := m.store.CreateNetwork(ctx, r); err != nil {
		return nil, err
	}

	return networkFromRecord(r, m.cni, m.store), nil
}

func (m *manager) Network(ctx context.Context, name string) (Network, error) {
	log.G(ctx).WithField("manager", m.name).WithField("network", name).Debugf("get network")

	r, err := m.store.GetNetwork(ctx, m.name, name)
	if err != nil {
		return nil, err
	}

	return networkFromRecord(r, m.cni, m.store), nil
}

func (m *manager) List(ctx context.Context) []Network {
	log.G(ctx).WithField("manager", m.name).Debugf("list networks")

	var networks []Network

	if err := m.store.WalkNetworks(ctx, m.name, func(r *networkRecord) error {
		networks = append(networks, networkFromRecord(r, m.cni, m.store))
		return nil

	}); err != nil {
		log.G(ctx).WithError(err).Errorf("failed to walk networks")
		return nil
	}

	return networks
}

func (m *manager) Attachment(ctx context.Context, id string) (Attachment, error) {
	log.G(ctx).WithFields(logrus.Fields{"manager": m.name, "attachment": id}).Debugf("get attachment")

	ar, err := m.store.GetAttachment(ctx, m.name, id)
	if err != nil {
		return nil, err
	}
	return attachmentFromRecord(ar, m.cni, m.store), nil
}
