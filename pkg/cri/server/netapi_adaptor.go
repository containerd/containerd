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

package server

import (
	"context"
	"reflect"
	"runtime"
	"strings"

	"github.com/containerd/containerd/pkg/net"
	"github.com/containerd/containerd/pkg/net/compat"
	"github.com/containerd/go-cni"
)

// cniAdaptor is created to adapt the APIs of network plugin to their
// counterparts in go-cni.
type cniAdaptor struct {
	adapt bool
	g     cni.CNI
	c     compat.CNI
}

var _ compat.CNI = (*cniAdaptor)(nil)

//nolint:nolintlint,unused
func newCNIAdaptor(netAPI compat.API, name string, opts ...compat.Opt) (*cniAdaptor, error) {
	var err error

	c := &cniAdaptor{
		adapt: false,
	}

	if netAPI == nil {
		c.adapt = true
	}

	if c.adapt {
		dopts, err := convertOpts(opts)
		if err != nil {
			return nil, err
		}
		if c.g, err = cni.New(dopts...); err != nil {
			return nil, err
		}
	} else {
		if c.c, err = netAPI.NewCNI(name, opts...); err != nil {
			return nil, err
		}
	}

	return c, nil
}

func (c *cniAdaptor) Setup(ctx context.Context, id string, path string, opts ...net.AttachmentOpt) (*compat.Result, error) {
	if !c.adapt {
		return c.c.Setup(ctx, id, path, opts...)
	}

	dopts, err := convertNamespaceOpts(opts)
	if err != nil {
		return nil, err
	}

	r, err := c.g.Setup(ctx, id, path, dopts...)
	if err != nil {
		return nil, err
	}

	return compat.WrapResult(r), nil
}

func (c *cniAdaptor) SetupSerially(ctx context.Context, id string, path string, opts ...net.AttachmentOpt) (*compat.Result, error) {
	if !c.adapt {
		return c.c.SetupSerially(ctx, id, path, opts...)
	}

	dopts, err := convertNamespaceOpts(opts)
	if err != nil {
		return nil, err
	}

	r, err := c.g.Setup(ctx, id, path, dopts...)
	if err != nil {
		return nil, err
	}

	return compat.WrapResult(r), nil
}

func (c *cniAdaptor) Remove(ctx context.Context, id string, path string, opts ...net.AttachmentOpt) error {
	if !c.adapt {
		return c.c.Remove(ctx, id, path, opts...)
	}

	dopts, err := convertNamespaceOpts(opts)
	if err != nil {
		return err
	}

	return c.g.Remove(ctx, id, path, dopts...)
}

func (c *cniAdaptor) Check(ctx context.Context, id string, path string, opts ...net.AttachmentOpt) error {
	if !c.adapt {
		return c.c.Check(ctx, id, path, opts...)
	}

	dopts, err := convertNamespaceOpts(opts)
	if err != nil {
		return err
	}

	return c.g.Check(ctx, id, path, dopts...)
}

func (c *cniAdaptor) Load(ctx context.Context, opts ...compat.LoadOpt) error {
	if !c.adapt {
		return c.c.Load(ctx, opts...)
	}

	var dopts []cni.Opt
	for _, o := range opts {
		name := getFunctionName(o)
		sl := strings.Split(name, "/")
		switch sl[len(sl)-1] {
		case "compat.WithLoNetwork":
			dopts = append(dopts, cni.WithLoNetwork)
		case "compat.WithDefaultConf":
			dopts = append(dopts, cni.WithDefaultConf)
		}
	}

	return c.g.Load(dopts...)
}

func (c *cniAdaptor) Status(ctx context.Context) error {
	if !c.adapt {
		return c.c.Status(ctx)
	}
	return c.g.Status()
}

func (c *cniAdaptor) GetConfig(ctx context.Context) *cni.ConfigResult {
	if !c.adapt {
		return c.c.GetConfig(ctx)
	}
	return c.g.GetConfig()
}

//nolint:nolintlint,unused
func convertOpts(opts []compat.Opt) ([]cni.Opt, error) {
	var (
		cfg   compat.Config
		dopts []cni.Opt
	)

	for _, o := range opts {
		if err := o(&cfg); err != nil {
			return dopts, err
		}
	}

	if len(cfg.PluginDirs) > 0 {
		dopts = append(dopts, cni.WithPluginDir(cfg.PluginDirs))
	}
	if len(cfg.PluginConfDir) > 0 {
		dopts = append(dopts, cni.WithPluginConfDir(cfg.PluginConfDir))
	}
	if cfg.PluginMaxConfNum > 0 {
		dopts = append(dopts, cni.WithPluginMaxConfNum(cfg.PluginMaxConfNum))
	}
	if len(cfg.Prefix) > 0 {
		dopts = append(dopts, cni.WithInterfacePrefix(cfg.Prefix))
	}
	if cfg.NetworkCount > 0 {
		dopts = append(dopts, cni.WithMinNetworkCount(cfg.NetworkCount))
	}

	return dopts, nil
}

func convertNamespaceOpts(opts []net.AttachmentOpt) ([]cni.NamespaceOpts, error) {
	var dopts []cni.NamespaceOpts

	args := net.AttachmentArgs{
		CapabilityArgs: make(map[string]interface{}),
		PluginArgs:     make(map[string]string),
	}

	for _, o := range opts {
		if err := o(&args); err != nil {
			return dopts, err
		}
	}

	for k, v := range args.PluginArgs {
		dopts = append(dopts, cni.WithArgs(k, v))
	}

	for k, v := range args.CapabilityArgs {
		dopts = append(dopts, cni.WithCapability(k, v))
	}

	return dopts, nil
}

func getFunctionName(i interface{}) string {
	return runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
}
