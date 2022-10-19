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

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/sandbox"
	"github.com/containerd/typeurl"
	"github.com/opencontainers/runtime-spec/specs-go"
)

type NewSandboxOpt func(ctx context.Context, client *Client, instance *sandbox.Sandbox) error

type DelSandboxOpt func(ctx context.Context, client *Client, instance *sandbox.Sandbox) error

type SimpleSpecOpts func(context.Context, string, *specs.Spec) error

// for the purpose of reuse container opts
func Simple(opts oci.SpecOpts) SimpleSpecOpts {
	return func(ctx context.Context, id string, spec *specs.Spec) error {
		c := &containers.Container{ID: id}
		return opts(ctx, nil, c, spec)
	}
}

func WithSandboxSpec(spec *specs.Spec, opts ...SimpleSpecOpts) NewSandboxOpt {
	return func(ctx context.Context, client *Client, instance *sandbox.Sandbox) error {
		for _, o := range opts {
			if err := o(ctx, instance.ID, spec); err != nil {
				return err
			}
		}
		instance.Spec = spec
		return nil
	}
}

func WithSandboxExtension(name string, ext interface{}) NewSandboxOpt {
	return func(ctx context.Context, client *Client, instance *sandbox.Sandbox) error {
		if instance.Extensions == nil {
			instance.Extensions = make(map[string]typeurl.Any)
		}

		a, err := typeurl.MarshalAny(ext)
		if err != nil {
			return fmt.Errorf("failed to marshal sandbox extension: %v", err)
		}

		instance.Extensions[name] = a
		return nil
	}
}

func WithSandboxLabels(labels map[string]string) NewSandboxOpt {
	return func(ctx context.Context, client *Client, opinstance *sandbox.Sandbox) error {
		if opinstance.Labels == nil {
			opinstance.Labels = make(map[string]string)
		}

		for k, v := range labels {
			opinstance.Labels[k] = v
		}

		return nil
	}
}
