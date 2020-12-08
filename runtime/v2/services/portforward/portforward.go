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

package portforward

import (
	"context"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/plugin"
	v2 "github.com/containerd/containerd/runtime/v2"
	"github.com/containerd/containerd/runtime/v2/services"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.ServicePlugin,
		ID:   services.PortForwardService,
		Requires: []plugin.Type{
			plugin.RuntimePluginV2,
		},
		InitFn: initFunc,
	})
}

func initFunc(ic *plugin.InitContext) (interface{}, error) {
	// PortForward is only supported by shims via runtime v2.
	v2r, err := ic.Get(plugin.RuntimePluginV2)
	if err != nil {
		return nil, err
	}

	pf := &portForward{
		v2Runtime: v2r.(*v2.TaskManager),
	}

	return pf, nil
}

type PortForward interface {
	PortForward(context.Context, PortForwardOpts) error
}

type PortForwardOpts struct {
	ID   string
	Port int32
	Addr string
}

type portForward struct {
	v2Runtime *v2.TaskManager
}

// PortForward implements PortForward.PortForward
func (p *portForward) PortForward(ctx context.Context, opts PortForwardOpts) error {
	// Get shim that implements port forwarding based on container ID
	pf, err := p.getPortForward(ctx, opts.ID)
	if err != nil {
		return err
	}

	// Forward the request to the shim.
	if err := pf.PortForward(ctx, v2.PortForwardOpts{
		Port: opts.Port,
		Addr: opts.Addr,
	}); err != nil {
		return errdefs.ToGRPC(err)
	}

	return nil
}

// getPortForward gets the port forwarding shim.
func (p *portForward) getPortForward(ctx context.Context, id string) (v2.PortForward, error) {
	t, err := p.v2Runtime.Get(ctx, id)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "runtime v2 task %v not found", id)
	}

	pf, ok := t.(v2.PortForward)
	if !ok {
		return nil, status.Errorf(codes.Unimplemented, "task %v does not support port forwarding", id)
	}

	return pf, nil
}
