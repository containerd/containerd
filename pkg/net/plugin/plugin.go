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

package plugin

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/pkg/net"
	"github.com/containerd/containerd/pkg/net/compat"
	"github.com/containerd/containerd/plugin"
	bolt "go.etcd.io/bbolt"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.NetworkPlugin,
		ID:   "cni",
		Requires: []plugin.Type{
			plugin.MetadataPlugin,
		},
		InitFn: initFunc,
	})
}

func initFunc(ic *plugin.InitContext) (interface{}, error) {
	m, err := ic.Get(plugin.MetadataPlugin)
	if err != nil {
		return nil, err
	}

	db, err := initDB(ic)
	if err != nil {
		return nil, err
	}

	i := &impl{
		db:   db,
		mgrs: make(map[string]net.Manager),
		cnis: make(map[string]compat.CNI),
	}

	collector := net.NewResourceCollector(i, db)
	m.(*metadata.DB).RegisterCollectibleResource(metadata.ResourceNetwork, collector)

	return i, nil
}

// impl models both network plugin and go-cni style interfaces
// client can freely choose any interface that's better suits
// the use cases.
type impl struct {
	db   net.DB
	mgrs map[string]net.Manager
	cnis map[string]compat.CNI
	lock sync.Mutex
}

// impl should implement the new network API, and also be compabitable with the gocni API
var _ net.API = (*impl)(nil)
var _ compat.API = (*impl)(nil)

func (i *impl) NewManager(name string, opts ...net.ManagerOpt) (net.Manager, error) {
	i.lock.Lock()
	defer i.lock.Unlock()

	if _, ok := i.mgrs[name]; ok {
		return nil, errdefs.ErrAlreadyExists
	}

	store, err := net.NewStore(i.db)
	if err != nil {
		return nil, err
	}

	m, err := net.NewManager(name, store, opts...)
	if err != nil {
		return nil, err
	}

	i.mgrs[name] = m
	return m, nil
}

func (i *impl) Manager(name string) net.Manager {
	i.lock.Lock()
	defer i.lock.Unlock()

	if m, ok := i.mgrs[name]; ok {
		return m
	}
	return nil
}

func (i *impl) NewCNI(name string, opts ...compat.Opt) (compat.CNI, error) {
	i.lock.Lock()
	defer i.lock.Unlock()

	if _, ok := i.cnis[name]; ok {
		return nil, errdefs.ErrAlreadyExists
	}
	if _, ok := i.mgrs[name]; ok {
		return nil, errdefs.ErrAlreadyExists
	}

	c := compat.Config{}
	for _, o := range opts {
		if err := o(&c); err != nil {
			return nil, err
		}
	}

	store, err := net.NewStore(i.db)
	if err != nil {
		return nil, err
	}

	m, err := net.NewManager(name, store, net.WithPluginDirs(c.PluginDirs...))
	if err != nil {
		return nil, err
	}

	cni, err := compat.New(m, opts...)
	if err != nil {
		return nil, err
	}

	i.cnis[name] = cni
	i.mgrs[name] = m

	return cni, nil
}

func (i *impl) CNI(name string) compat.CNI {
	i.lock.Lock()
	defer i.lock.Unlock()

	if cni, ok := i.cnis[name]; ok {
		return cni
	}
	return nil
}

func initDB(ic *plugin.InitContext) (*bolt.DB, error) {
	if err := os.MkdirAll(ic.State, 0711); err != nil {
		return nil, err
	}
	path := filepath.Join(ic.State, "net.db")
	options := *bolt.DefaultOptions
	// Reading bbolt's freelist sometimes fails when the file has a data corruption.
	// Disabling freelist sync reduces the chance of the breakage.
	// https://github.com/etcd-io/bbolt/pull/1
	// https://github.com/etcd-io/bbolt/pull/6
	options.NoFreelistSync = true
	// Without the timeout, bbolt.Open would block indefinitely due to flock(2).
	options.Timeout = 1

	doneCh := make(chan struct{})
	go func() {
		t := time.NewTimer(10 * time.Second)
		defer t.Stop()
		select {
		case <-t.C:
			log.G(ic.Context).WithField("plugin", "bolt").Warn("waiting for response from boltdb open")
		case <-doneCh:
			return
		}
	}()

	db, err := bolt.Open(path, 0644, &options)
	close(doneCh)
	return db, err
}
