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

package cni

import (
	"fmt"
	"sync"

	cnilibrary "github.com/containernetworking/cni/libcni"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/pkg/errors"
)

type CNI interface {
	// Setup setup the network for the namespace
	Setup(id string, path string, opts ...NamespaceOpts) (*CNIResult, error)
	// Remove tears down the network of the namespace.
	Remove(id string, path string, opts ...NamespaceOpts) error
	// Load loads the cni network config
	Load(opts ...LoadOption) error
	// Status checks the status of the cni initialization
	Status() error
}

type libcni struct {
	config

	cniConfig    cnilibrary.CNI
	networkCount int // minimum network plugin configurations needed to initialize cni
	networks     []*Network
	sync.RWMutex
}

func defaultCNIConfig() *libcni {
	return &libcni{
		config: config{
			pluginDirs:    []string{DefaultCNIDir},
			pluginConfDir: DefaultNetDir,
			prefix:        DefaultPrefix,
		},
		cniConfig: &cnilibrary.CNIConfig{
			Path: []string{DefaultCNIDir},
		},
		networkCount: 1,
	}
}

func New(config ...ConfigOption) (CNI, error) {
	cni := defaultCNIConfig()
	var err error
	for _, c := range config {
		if err = c(cni); err != nil {
			return nil, err
		}
	}
	return cni, nil
}

func (c *libcni) Load(opts ...LoadOption) error {
	var err error
	// Reset the networks on a load operation to ensure
	// config happens on a clean slate
	c.reset()

	for _, o := range opts {
		if err = o(c); err != nil {
			return errors.Wrapf(ErrLoad, fmt.Sprintf("cni config load failed: %v", err))
		}
	}
	return c.Status()
}

func (c *libcni) Status() error {
	c.RLock()
	defer c.RUnlock()
	if len(c.networks) < c.networkCount {
		return ErrCNINotInitialized
	}
	return nil
}

// Setup setups the network in the namespace
func (c *libcni) Setup(id string, path string, opts ...NamespaceOpts) (*CNIResult, error) {
	if err:=c.Status();err!=nil{
		return nil,err
	}
	ns, err := newNamespace(id, path, opts...)
	if err != nil {
		return nil, err
	}
	var results []*current.Result
	c.RLock()
	defer c.RUnlock()
	for _, network := range c.networks {
		r, err := network.Attach(ns)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return c.GetCNIResultFromResults(results)
}

// Remove removes the network config from the namespace
func (c *libcni) Remove(id string, path string, opts ...NamespaceOpts) error {
	if err:=c.Status();err!=nil{
           return err
        }
	ns, err := newNamespace(id, path, opts...)
	if err != nil {
		return err
	}
	c.RLock()
	defer c.RUnlock()
	for _, network := range c.networks {
		if err := network.Remove(ns); err != nil {
			return err
		}
	}
	return nil
}

func (c *libcni) reset() {
	c.Lock()
	defer c.Unlock()
	c.networks = nil
}
