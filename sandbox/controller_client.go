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

package sandbox

import (
	"context"

	api "github.com/containerd/containerd/api/services/sandbox/v1"
	"github.com/containerd/containerd/errdefs"
)

// controllerClient is a client used by containerd to communicate with a sandbox proxy plugin
type controllerClient struct {
	client api.ControllerClient
}

var _ Controller = &controllerClient{}

func NewSandboxController(client api.ControllerClient) Controller {
	return &controllerClient{client: client}
}

func (p *controllerClient) Start(ctx context.Context, sandbox *Sandbox) (*Sandbox, error) {
	inst, err := ToProto(sandbox)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Start(ctx, &api.ControllerStartRequest{Sandbox: inst})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}

	return FromProto(resp.Sandbox)
}
func (p *controllerClient) Shutdown(ctx context.Context, id string) error {
	_, err := p.client.Shutdown(ctx, &api.ControllerShutdownRequest{ID: id})
	if err != nil {
		return errdefs.FromGRPC(err)
	}

	return nil
}

func (p *controllerClient) Pause(ctx context.Context, id string) error {
	_, err := p.client.Pause(ctx, &api.ControllerPauseRequest{SandboxID: id})
	if err != nil {
		return errdefs.FromGRPC(err)
	}

	return nil
}

func (p *controllerClient) Resume(ctx context.Context, id string) error {
	_, err := p.client.Resume(ctx, &api.ControllerResumeRequest{SandboxID: id})
	if err != nil {
		return errdefs.FromGRPC(err)
	}

	return nil
}

func (p *controllerClient) Update(ctx context.Context, sandboxID string, sandbox *Sandbox) (*Sandbox, error) {
	inst, err := ToProto(sandbox)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Update(ctx, &api.ControllerUpdateRequest{Sandbox: inst})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}

	return FromProto(resp.Sandbox)
}

func (p *controllerClient) Ping(ctx context.Context, id string) error {
	_, err := p.client.Ping(ctx, &api.ControllerPingRequest{SandboxID: id})
	if err != nil {
		return errdefs.FromGRPC(err)
	}

	return nil
}

func (p *controllerClient) AppendContainer(ctx context.Context, sandboxID string, container *Container) (*Container, error) {
	proto, err := ContainerToProto(*container)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.AppendContainer(ctx, &api.ControllerAppendContainerRequest{
		SandboxID: sandboxID,
		Container: proto,
	})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	c, err := ContainerFromProto(resp.Container)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (p *controllerClient) UpdateContainer(ctx context.Context, sandboxID string, container *Container) (*Container, error) {
	proto, err := ContainerToProto(*container)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.UpdateContainer(ctx, &api.ControllerUpdateContainerRequest{
		SandboxID: sandboxID,
		Container: proto,
	})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	c, err := ContainerFromProto(resp.Container)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (p *controllerClient) RemoveContainer(ctx context.Context, sandboxID string, id string) error {
	_, err := p.client.RemoveContainer(ctx, &api.ControllerRemoveContainerRequest{
		SandboxID:   sandboxID,
		ContainerID: id,
	})
	return errdefs.FromGRPC(err)
}

func (p *controllerClient) Status(ctx context.Context, id string) (Status, error) {
	resp, err := p.client.Status(ctx, &api.ControllerStatusRequest{
		ID: id,
	})
	if err != nil {
		return Status{}, errdefs.FromGRPC(err)
	}

	return StatusFromProto(resp.Status), nil
}
