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
	"time"

	api "github.com/containerd/containerd/sandbox"
	"github.com/containerd/typeurl"
	"github.com/gogo/protobuf/types"
	"github.com/pkg/errors"
)

// Sandbox is a high level client to containerd's sandboxes.
type Sandbox interface {
	// ID is a sandbox identifier
	ID() string
	// NewContainer creates new container that will belong to this sandbox
	NewContainer(ctx context.Context, id string, opts ...NewContainerOpts) (Container, error)
	// Labels returns the labels set on the sandbox
	Labels(ctx context.Context) (map[string]string, error)
	// Start starts new sandbox instance
	Start(ctx context.Context) error
	// Shutdown will turn down existing sandbox instance.
	// If using force, the client will ignore shutdown errors.
	Shutdown(ctx context.Context, force bool) error
	// Pause will freeze running sandbox instance
	Pause(ctx context.Context) error
	// Resume will unfreeze previously paused sandbox instance
	Resume(ctx context.Context) error
	// Status will return current sandbox status (provided by shim runtime)
	Status(ctx context.Context, status interface{}) error
	// Ping will check whether existing sandbox instance alive
	Ping(ctx context.Context) error
}

type sandboxClient struct {
	client   *Client
	metadata api.Sandbox
}

func (s *sandboxClient) ID() string {
	return s.metadata.ID
}

func (s *sandboxClient) NewContainer(ctx context.Context, id string, opts ...NewContainerOpts) (Container, error) {
	return s.client.NewContainer(ctx, id, append(opts, WithSandbox(s.ID()))...)
}

func (s *sandboxClient) Labels(ctx context.Context) (map[string]string, error) {
	sandbox, err := s.client.SandboxStore().Get(ctx, s.ID())
	if err != nil {
		return nil, err
	}

	return sandbox.Labels, nil
}

func (s *sandboxClient) Start(ctx context.Context) error {
	return s.client.SandboxController().Start(ctx, s.ID())
}

func (s *sandboxClient) Shutdown(ctx context.Context, force bool) error {
	var (
		controller = s.client.SandboxController()
		store      = s.client.SandboxStore()
	)

	err := controller.Shutdown(ctx, s.ID())
	if err != nil && !force {
		return fmt.Errorf("failed to shutdown sandbox: %w", err)
	}

	err = store.Delete(ctx, s.ID())
	if err != nil {
		return fmt.Errorf("failed to delete sandbox from metadata store: %w", err)
	}

	return nil
}

func (s *sandboxClient) Pause(ctx context.Context) error {
	return s.client.SandboxController().Pause(ctx, s.ID())
}

func (s *sandboxClient) Resume(ctx context.Context) error {
	return s.client.SandboxController().Resume(ctx, s.ID())
}

func (s *sandboxClient) Ping(ctx context.Context) error {
	return s.client.SandboxController().Ping(ctx, s.ID())
}

func (s *sandboxClient) Status(ctx context.Context, status interface{}) error {
	any, err := s.client.SandboxController().Status(ctx, s.ID())
	if err != nil {
		return err
	}

	if err := typeurl.UnmarshalTo(any, status); err != nil {
		return errors.Wrap(err, "failed to unmarshal sandbox status")
	}

	return nil
}

// NewSandbox creates new sandbox client
func (c *Client) NewSandbox(ctx context.Context, sandboxID string, opts ...NewSandboxOpts) (Sandbox, error) {
	if sandboxID == "" {
		return nil, errors.New("sandbox ID must be specified")
	}

	newSandbox := api.Sandbox{
		ID:        sandboxID,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	for _, opt := range opts {
		if err := opt(ctx, c, &newSandbox); err != nil {
			return nil, err
		}
	}

	metadata, err := c.SandboxStore().Create(ctx, newSandbox)
	if err != nil {
		return nil, err
	}

	return &sandboxClient{
		client:   c,
		metadata: metadata,
	}, nil
}

// LoadSandbox laods existing sandbox metadata object using the id
func (c *Client) LoadSandbox(ctx context.Context, id string) (Sandbox, error) {
	sandbox, err := c.SandboxStore().Get(ctx, id)
	if err != nil {
		return nil, err
	}

	return &sandboxClient{
		client:   c,
		metadata: sandbox,
	}, nil
}

// NewSandboxOpts is a sandbox options and extensions to be provided by client
type NewSandboxOpts func(ctx context.Context, client *Client, sandbox *api.Sandbox) error

// WithSandboxRuntime allows a user to specify the runtime to be used to run a sandbox
func WithSandboxRuntime(name string, options interface{}) NewSandboxOpts {
	return func(ctx context.Context, client *Client, s *api.Sandbox) error {
		if options == nil {
			options = &types.Empty{}
		}

		opts, err := typeurl.MarshalAny(options)
		if err != nil {
			return errors.Wrap(err, "failed to marshal sandbox runtime options")
		}

		s.Runtime = api.RuntimeOpts{
			Name:    name,
			Options: opts,
		}

		return nil
	}
}

// WithSandboxSpec will provide the sandbox runtime spec
func WithSandboxSpec(spec interface{}) NewSandboxOpts {
	return func(ctx context.Context, client *Client, sandbox *api.Sandbox) error {
		spec, err := typeurl.MarshalAny(spec)
		if err != nil {
			return errors.Wrap(err, "failed to marshal spec")
		}

		sandbox.Spec = spec
		return nil
	}
}

// WithSandboxExtension attaches an extension to sandbox
func WithSandboxExtension(name string, ext interface{}) NewSandboxOpts {
	return func(ctx context.Context, client *Client, s *api.Sandbox) error {
		if s.Extensions == nil {
			s.Extensions = make(map[string]types.Any)
		}

		any, err := typeurl.MarshalAny(ext)
		if err != nil {
			return errors.Wrap(err, "failed to marshal sandbox extension")
		}

		s.Extensions[name] = *any
		return err
	}
}

// WithSandboxLabels attaches map of labels to sandbox
func WithSandboxLabels(labels map[string]string) NewSandboxOpts {
	return func(ctx context.Context, client *Client, sandbox *api.Sandbox) error {
		sandbox.Labels = labels
		return nil
	}
}
