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

package containerd

import (
	"context"
	"fmt"
	"strings"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/sandbox"
	"github.com/containerd/typeurl"
	"github.com/pkg/errors"
)

type Sandbox interface {
	// ID returns unique sandbox instance identifier
	ID() string
	// Status returns current sandbox instance status
	Status(ctx context.Context) (sandbox.Status, error)
	// Metadata returns the underlying sandbox instance metadata
	Metadata(ctx context.Context) (*sandbox.Sandbox, error)
	// Labels returns the underlying labels associated with the instance
	Labels(ctx context.Context) (map[string]string, error)
	// SetLabels sets the provided labels for the sandbox and returns the final label set
	SetLabels(context.Context, map[string]string) (map[string]string, error)
	// Extensions returns the underlying extensions associated with the instnace
	Extensions(ctx context.Context) (map[string]typeurl.Any, error)
	// Delete deletes a sandbox instance and removes it from containerd metadata store
	Delete(ctx context.Context, opts ...DelSandboxOpt) error
}

type sandboxInstance struct {
	name   string
	id     string
	client sandbox.Sandboxer
}

var _ Sandbox = &sandboxInstance{}

func (s *sandboxInstance) ID() string {
	return s.id
}

func (s *sandboxInstance) Status(ctx context.Context) (sandbox.Status, error) {
	return s.client.Status(ctx, s.ID())
}

func (s *sandboxInstance) Metadata(ctx context.Context) (*sandbox.Sandbox, error) {
	info, err := s.client.Get(ctx, s.ID())
	if err != nil {
		return nil, err
	}

	return info, nil
}

func (s *sandboxInstance) Labels(ctx context.Context) (map[string]string, error) {
	info, err := s.Metadata(ctx)
	if err != nil {
		return nil, err
	}

	return info.Labels, nil
}

func (s *sandboxInstance) SetLabels(ctx context.Context, labels map[string]string) (map[string]string, error) {
	instance := &sandbox.Sandbox{
		ID:     s.ID(),
		Labels: labels,
	}

	var paths []string
	for k := range labels {
		paths = append(paths, strings.Join([]string{"labels", k}, "."))
	}

	resp, err := s.client.Update(ctx, instance, paths...)
	if err != nil {
		return nil, err
	}

	return resp.Labels, nil
}

func (s *sandboxInstance) Extensions(ctx context.Context) (map[string]typeurl.Any, error) {
	info, err := s.Metadata(ctx)
	if err != nil {
		return nil, err
	}

	return info.Extensions, nil
}

func (s *sandboxInstance) Delete(ctx context.Context, _ ...DelSandboxOpt) error {
	return s.client.Delete(ctx, s.ID())
}

func (c *Client) NewSandbox(ctx context.Context, name, id string, opts ...NewSandboxOpt) (Sandbox, error) {
	in := sandbox.Sandbox{
		ID: id,
	}

	for _, opt := range opts {
		if err := opt(ctx, c, &in); err != nil {
			return nil, err
		}
	}

	client := c.SandboxService(name)
	if client == nil {
		return nil, errors.Wrapf(errdefs.ErrNotFound, "no sandboxer named %s", name)
	}
	out, err := client.Create(ctx, &in)
	if err != nil {
		return nil, err
	}

	return &sandboxInstance{
		name:   name,
		id:     out.ID,
		client: client,
	}, nil
}

func (c *Client) LoadSandbox(ctx context.Context, name, id string) (Sandbox, error) {
	client := c.SandboxService(name)
	if client == nil {
		return nil, fmt.Errorf("failed to get sandboxer by name %s, %w", name, errdefs.ErrNotFound)
	}
	info, err := client.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to query sandbox info for %q: %w", id, err)
	}

	return &sandboxInstance{
		name:   name,
		id:     info.ID,
		client: client,
	}, nil
}

// AllSandboxes returns all sandboxes created in containerd
func (c *Client) AllSandboxes(ctx context.Context) (map[string][]sandbox.Sandbox, error) {
	ret := make(map[string][]sandbox.Sandbox)
	for name, sandboxer := range c.sandboxers {
		sandboxes, err := sandboxer.List(ctx)
		if err != nil && !errdefs.IsNotFound(err) {
			return ret, err
		}
		ret[name] = sandboxes
	}
	return ret, nil
}
