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

package adaptation

import (
	"context"

	"github.com/containerd/nri/pkg/api"
)

type pluginType struct {
	wasmImpl  api.Plugin
	ttrpcImpl api.PluginService
}

func (p *pluginType) isWasm() bool {
	return p.wasmImpl != nil
}

func (p *pluginType) isTtrpc() bool {
	return p.ttrpcImpl != nil
}

func (p *pluginType) Synchronize(ctx context.Context, req *SynchronizeRequest) (*SynchronizeResponse, error) {
	if p.wasmImpl != nil {
		return p.wasmImpl.Synchronize(ctx, req)
	}
	return p.ttrpcImpl.Synchronize(ctx, req)
}

func (p *pluginType) Configure(ctx context.Context, req *ConfigureRequest) (*ConfigureResponse, error) {
	if p.wasmImpl != nil {
		return p.wasmImpl.Configure(ctx, req)
	}
	return p.ttrpcImpl.Configure(ctx, req)
}

func (p *pluginType) CreateContainer(ctx context.Context, req *CreateContainerRequest) (*CreateContainerResponse, error) {
	if p.wasmImpl != nil {
		return p.wasmImpl.CreateContainer(ctx, req)
	}
	return p.ttrpcImpl.CreateContainer(ctx, req)
}

func (p *pluginType) UpdateContainer(ctx context.Context, req *UpdateContainerRequest) (*UpdateContainerResponse, error) {
	if p.wasmImpl != nil {
		return p.wasmImpl.UpdateContainer(ctx, req)
	}
	return p.ttrpcImpl.UpdateContainer(ctx, req)
}

func (p *pluginType) StopContainer(ctx context.Context, req *StopContainerRequest) (*StopContainerResponse, error) {
	if p.wasmImpl != nil {
		return p.wasmImpl.StopContainer(ctx, req)
	}
	return p.ttrpcImpl.StopContainer(ctx, req)
}

func (p *pluginType) StateChange(ctx context.Context, req *StateChangeEvent) (err error) {
	if p.wasmImpl != nil {
		_, err = p.wasmImpl.StateChange(ctx, req)
	} else {
		_, err = p.ttrpcImpl.StateChange(ctx, req)
	}
	return err
}
