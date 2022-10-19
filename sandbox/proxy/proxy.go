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

package proxy

import (
	"context"

	sandboxapi "github.com/containerd/containerd/api/services/sandbox/v1"
	"github.com/containerd/containerd/sandbox"
)

func NewSandboxer(client sandboxapi.SandboxerClient, sandboxerName string) sandbox.Sandboxer {
	return &proxySandboxer{
		client:        client,
		sandboxerName: sandboxerName,
	}
}

type proxySandboxer struct {
	client        sandboxapi.SandboxerClient
	sandboxerName string
}

func (p *proxySandboxer) Create(ctx context.Context, sb *sandbox.Sandbox) (*sandbox.Sandbox, error) {
	apiSandbox, err := sandbox.ToProto(sb)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Create(ctx, &sandboxapi.CreateSandboxRequest{
		Name:    p.sandboxerName,
		Sandbox: apiSandbox,
	})
	if err != nil {
		return nil, err
	}
	return sandbox.FromProto(resp.Sandbox)
}

func (p *proxySandboxer) Update(ctx context.Context, sb *sandbox.Sandbox, fieldpaths ...string) (*sandbox.Sandbox, error) {
	apiSandbox, err := sandbox.ToProto(sb)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Update(ctx, &sandboxapi.UpdateSandboxRequest{
		Name:    p.sandboxerName,
		Sandbox: apiSandbox,
	})
	if err != nil {
		return nil, err
	}
	return sandbox.FromProto(resp.Sandbox)
}

func (p *proxySandboxer) AppendContainer(ctx context.Context, sandboxID string, container *sandbox.Container) (*sandbox.Container, error) {
	apiContainer, err := sandbox.ContainerToProto(*container)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.AppendContainer(ctx, &sandboxapi.AppendContainerRequest{
		Name:      p.sandboxerName,
		SandboxID: sandboxID,
		Container: apiContainer,
	})
	if err != nil {
		return nil, err
	}
	newCont, err := sandbox.ContainerFromProto(resp.Container)
	if err != nil {
		return nil, err
	}
	return &newCont, nil
}

func (p *proxySandboxer) UpdateContainer(ctx context.Context, sandboxID string, container *sandbox.Container) (*sandbox.Container, error) {
	apiContainer, err := sandbox.ContainerToProto(*container)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.UpdateContainer(ctx, &sandboxapi.UpdateContainerRequest{
		Name:      p.sandboxerName,
		SandboxID: sandboxID,
		Container: apiContainer,
	})
	if err != nil {
		return nil, err
	}
	newCont, err := sandbox.ContainerFromProto(resp.Container)
	if err != nil {
		return nil, err
	}
	return &newCont, nil
}

func (p *proxySandboxer) RemoveContainer(ctx context.Context, sandboxID string, id string) error {
	_, err := p.client.RemoveContainer(ctx, &sandboxapi.RemoveContainerRequest{
		Name:        p.sandboxerName,
		SandboxID:   sandboxID,
		ContainerID: id,
	})
	return err
}

func (p *proxySandboxer) Delete(ctx context.Context, id string) error {
	_, err := p.client.Delete(ctx, &sandboxapi.DeleteSandboxRequest{
		Name: p.sandboxerName,
		ID:   id,
	})
	return err
}

func (p *proxySandboxer) List(ctx context.Context, fields ...string) ([]sandbox.Sandbox, error) {
	resp, err := p.client.List(ctx, &sandboxapi.ListSandboxRequest{
		Name:    p.sandboxerName,
		Filters: fields,
	})
	if err != nil {
		return nil, err
	}
	var ret []sandbox.Sandbox
	for _, apiSandbox := range resp.List {
		s, err := sandbox.FromProto(apiSandbox)
		if err != nil {
			return nil, err
		}
		ret = append(ret, *s)
	}
	return ret, nil
}

func (p *proxySandboxer) Get(ctx context.Context, id string) (*sandbox.Sandbox, error) {
	resp, err := p.client.Get(ctx, &sandboxapi.GetSandboxRequest{
		Name: p.sandboxerName,
		ID:   id,
	})
	if err != nil {
		return nil, err
	}
	return sandbox.FromProto(resp.Sandbox)
}

func (p *proxySandboxer) Status(ctx context.Context, id string) (sandbox.Status, error) {
	resp, err := p.client.Status(ctx, &sandboxapi.StatusRequest{
		Name: p.sandboxerName,
		ID:   id,
	})
	if err != nil {
		return sandbox.Status{}, err
	}
	return sandbox.StatusFromProto(resp.Status), nil
}
