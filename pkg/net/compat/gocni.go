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

package compat

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/containerd/containerd/pkg/net"
	gocni "github.com/containerd/go-cni"
	types100 "github.com/containernetworking/cni/pkg/types/100"
)

const (
	CNIPluginName        = "cni"
	DefaultNetDir        = "/etc/cni/net.d"
	DefaultCNIDir        = "/opt/cni/bin"
	DefaultMaxConfNum    = 1
	VendorCNIDirTemplate = "%s/opt/%s/bin"
	DefaultPrefix        = "eth"
	DefaultManagerName   = "default"
)

// API defines the APIs for clients to create/locate
// go-cni style objects
type API interface {
	NewCNI(name string, opts ...Opt) (CNI, error)
	CNI(name string) CNI
}

// CNI defines an interface which closely models the go-cni library.
// This interface is created to ease the effort of adapting the
// existing CRI implementation to the Network Plugin.
type CNI interface {
	Setup(ctx context.Context, id string, path string, opts ...net.AttachmentOpt) (*Result, error)
	SetupSerially(ctx context.Context, id string, path string, opts ...net.AttachmentOpt) (*Result, error)
	Remove(ctx context.Context, id string, path string, opts ...net.AttachmentOpt) error
	Check(ctx context.Context, id string, path string, opts ...net.AttachmentOpt) error
	Load(ctx context.Context, opts ...LoadOpt) error
	Status(ctx context.Context) error
	GetConfig(ctx context.Context) *gocni.ConfigResult
}

type Config struct {
	PluginDirs       []string
	PluginConfDir    string
	PluginMaxConfNum int
	Prefix           string
	NetworkCount     int // minimum network plugin configurations needed to initialize cni
}

type impl struct {
	Config
	m net.Manager
	sync.RWMutex
}

var _ CNI = (*impl)(nil)

func defaultCNIConfig() *impl {
	return &impl{
		Config: Config{
			PluginDirs:       []string{DefaultCNIDir},
			PluginConfDir:    DefaultNetDir,
			PluginMaxConfNum: DefaultMaxConfNum,
			Prefix:           DefaultPrefix,
			NetworkCount:     1,
		},
	}
}

func New(m net.Manager, config ...Opt) (CNI, error) {
	cni := defaultCNIConfig()

	var err error
	for _, c := range config {
		if err = c(&cni.Config); err != nil {
			return nil, err
		}
	}

	cni.m = m
	return cni, nil
}

func (c *impl) Setup(ctx context.Context, id string, path string, opts ...net.AttachmentOpt) (*Result, error) {
	if err := c.Status(ctx); err != nil {
		return nil, err
	}

	opts = append(opts, net.WithContainer(id), net.WithNSPath(path))

	results, err := c.attachNetworks(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return c.createResult(results)
}

type attachResult struct {
	r *types100.Result
	l map[string]string
}

type asynchAttachResult struct {
	index int
	res   attachResult
	err   error
}

func (c *impl) asynchAttach(ctx context.Context, index int, n net.Network, wg *sync.WaitGroup, rc chan asynchAttachResult, opts ...net.AttachmentOpt) {
	defer wg.Done()
	r, err := n.Attach(ctx, opts...)

	if err != nil {
		rc <- asynchAttachResult{index: index, err: err}
		return
	}

	rc <- asynchAttachResult{
		index: index,
		res: attachResult{
			r: r.Result(),
			l: r.GCOwnerLables(),
		},
		err: err,
	}
}

func (c *impl) attachNetworks(ctx context.Context, opts ...net.AttachmentOpt) ([]*attachResult, error) {
	var wg sync.WaitGroup
	var firstError error

	netlist := c.m.List(ctx)
	results := make([]*attachResult, len(netlist))
	rc := make(chan asynchAttachResult)

	for i, n := range netlist {
		wg.Add(1)
		// make a copy below so that `ifname` may be appended to the duplicated slice
		dopts := make([]net.AttachmentOpt, len(opts))
		copy(dopts, opts)
		if ifname, ok := n.Labels()["ifname"]; ok {
			dopts = append(dopts, net.WithIFName(ifname))
		}
		go c.asynchAttach(ctx, i, n, &wg, rc, dopts...)
	}

	for range netlist {
		rs := <-rc
		if rs.err != nil && firstError == nil {
			firstError = rs.err
		}
		results[rs.index] = &rs.res
	}
	wg.Wait()

	return results, firstError
}

func (c *impl) SetupSerially(ctx context.Context, id string, path string, opts ...net.AttachmentOpt) (*Result, error) {
	if err := c.Status(ctx); err != nil {
		return nil, err
	}
	opts = append(opts, net.WithContainer(id), net.WithNSPath(path))

	var results []*attachResult
	netlist := c.m.List(ctx)

	for _, n := range netlist {
		// make a copy below so that `ifname` may be appended to the duplicated slice
		dopts := make([]net.AttachmentOpt, len(opts))
		copy(dopts, opts)
		if ifname, ok := n.Labels()["ifname"]; ok {
			dopts = append(dopts, net.WithIFName(ifname))
		}
		att, err := n.Attach(ctx, dopts...)
		if err != nil {
			return nil, err
		}
		results = append(results, &attachResult{r: att.Result(), l: att.GCOwnerLables()})
	}
	return c.createResult(results)
}

func (c *impl) Remove(ctx context.Context, id string, path string, opts ...net.AttachmentOpt) error {
	if err := c.Status(ctx); err != nil {
		return err
	}
	netlist := c.m.List(ctx)

	for _, n := range netlist {
		attlist := n.List(ctx)

		for _, att := range attlist {
			if att.Container() != id {
				continue
			}
			if err := att.Remove(ctx); err != nil {
				// Based on CNI spec v0.7.0, empty network namespace is allowed to
				// do best effort cleanup. However, it is not handled consistently
				// right now:
				// https://github.com/containernetworking/plugins/issues/210
				// TODO(random-liu): Remove the error handling when the issue is
				// fixed and the CNI spec v0.6.0 support is deprecated.
				// NOTE(claudiub): Some CNIs could return a "not found" error, which could mean that
				// it was already deleted.
				if (att.NSPath() == "" && strings.Contains(err.Error(), "no such file or directory")) || strings.Contains(err.Error(), "not found") {
					continue
				}
				return err
			}
		}
	}
	return nil
}

func (c *impl) Check(ctx context.Context, id string, path string, opts ...net.AttachmentOpt) error {
	if err := c.Status(ctx); err != nil {
		return err
	}
	netlist := c.m.List(ctx)
	for _, n := range netlist {
		attlist := n.List(ctx)
		for _, att := range attlist {
			if att.Container() != id {
				continue
			}
			err := att.Check(ctx)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *impl) Load(ctx context.Context, opts ...LoadOpt) error {
	var err error
	c.Lock()
	defer c.Unlock()
	// Reset the net on a load operation to ensure
	// config happens on a clean slate
	c.reset(ctx)

	for _, o := range opts {
		if err = o(ctx, c); err != nil {
			return fmt.Errorf("cni config load failed: %v: %w", err, gocni.ErrLoad)
		}
	}
	return nil
}

func (c *impl) Status(ctx context.Context) error {
	c.RLock()
	defer c.RUnlock()
	nets := c.m.List(ctx)
	if len(nets) < c.NetworkCount {
		return gocni.ErrCNINotInitialized
	}
	return nil
}

func (c *impl) GetConfig(ctx context.Context) *gocni.ConfigResult {
	c.RLock()
	defer c.RUnlock()
	r := &gocni.ConfigResult{
		PluginDirs:       c.Config.PluginDirs,
		PluginConfDir:    c.Config.PluginConfDir,
		PluginMaxConfNum: c.Config.PluginMaxConfNum,
		Prefix:           c.Config.Prefix,
	}
	for _, network := range c.m.List(ctx) {
		conf := &gocni.NetworkConfList{
			Name:       network.Config().Name,
			CNIVersion: network.Config().CNIVersion,
			Source:     string(network.Config().Bytes),
		}
		for _, plugin := range network.Config().Plugins {
			conf.Plugins = append(conf.Plugins, &gocni.NetworkConf{
				Network: plugin.Network,
				Source:  string(plugin.Bytes),
			})
		}
		r.Networks = append(r.Networks, &gocni.ConfNetwork{
			Config: conf,
			IFName: network.Labels()["ifname"],
		})
	}
	return r
}

func (c *impl) reset(ctx context.Context) {
	netList := c.m.List(ctx)
	for _, n := range netList {
		n.Delete(ctx)
	}
}
