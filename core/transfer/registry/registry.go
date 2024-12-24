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

package registry

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	transfertypes "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/containerd/containerd/v2/core/remotes/docker/config"
	"github.com/containerd/containerd/v2/core/streaming"
	"github.com/containerd/containerd/v2/core/transfer"
	"github.com/containerd/containerd/v2/core/transfer/plugins"
	"github.com/containerd/containerd/v2/core/transfer/registry/auth"
	tstreaming "github.com/containerd/containerd/v2/core/transfer/streaming"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func init() {
	// TODO: Move this to separate package?
	plugins.Register(&transfertypes.OCIRegistry{}, &OCIRegistry{})
}

type registryOpts struct {
	headers       http.Header
	creds         auth.CredentialHelper
	hostDir       string
	defaultScheme string
}

// Opt sets registry-related configurations.
type Opt func(o *registryOpts) error

// WithHeaders configures HTTP request header fields sent by the resolver.
func WithHeaders(headers http.Header) Opt {
	return func(o *registryOpts) error {
		o.headers = headers
		return nil
	}
}

// WithCredentials configures a helper that provides credentials for a host.
func WithCredentials(creds auth.CredentialHelper) Opt {
	return func(o *registryOpts) error {
		o.creds = creds
		return nil
	}
}

// WithHostDir specifies the host configuration directory.
func WithHostDir(hostDir string) Opt {
	return func(o *registryOpts) error {
		o.hostDir = hostDir
		return nil
	}
}

// WithDefaultScheme specifies the default scheme for registry configuration
func WithDefaultScheme(s string) Opt {
	return func(o *registryOpts) error {
		o.defaultScheme = s
		return nil
	}
}

// NewOCIRegistry initializes with hosts, authorizer callback, and headers
func NewOCIRegistry(ctx context.Context, ref string, opts ...Opt) (*OCIRegistry, error) {
	var ropts registryOpts
	for _, o := range opts {
		if err := o(&ropts); err != nil {
			return nil, err
		}
	}
	hostOptions := config.HostOptions{}
	if ropts.hostDir != "" {
		hostOptions.HostDir = config.HostDirFromRoot(ropts.hostDir)
	}
	if ropts.creds != nil {
		// TODO: Support bearer
		hostOptions.Credentials = func(host string) (string, string, error) {
			c, err := ropts.creds.GetCredentials(context.Background(), ref, host)
			if err != nil {
				return "", "", err
			}

			return c.Username, c.Secret, nil
		}
	}
	if ropts.defaultScheme != "" {
		hostOptions.DefaultScheme = ropts.defaultScheme
	}
	resolver := docker.NewResolver(docker.ResolverOptions{
		Hosts:   config.ConfigureHosts(ctx, hostOptions),
		Headers: ropts.headers,
	})
	return &OCIRegistry{
		reference:     ref,
		headers:       ropts.headers,
		creds:         ropts.creds,
		resolver:      resolver,
		hostDir:       ropts.hostDir,
		defaultScheme: ropts.defaultScheme,
	}, nil
}

// OCI
type OCIRegistry struct {
	reference string

	headers http.Header
	creds   auth.CredentialHelper

	resolver remotes.Resolver

	hostDir string

	defaultScheme string

	// This could be an interface which returns resolver?
	// Resolver could also be a plug-able interface, to call out to a program to fetch?
}

func (r *OCIRegistry) String() string {
	return fmt.Sprintf("OCI Registry (%s)", r.reference)
}

func (r *OCIRegistry) Image() string {
	return r.reference
}

func (r *OCIRegistry) Resolve(ctx context.Context) (name string, desc ocispec.Descriptor, err error) {
	return r.resolver.Resolve(ctx, r.reference)
}

func (r *OCIRegistry) Fetcher(ctx context.Context, ref string) (transfer.Fetcher, error) {
	return r.resolver.Fetcher(ctx, ref)
}

func (r *OCIRegistry) GetCredentials(ctx context.Context, ref, host string) (transfer.Credentials, error) {
	return r.creds.GetCredentials(ctx, ref, host)
}

func (r *OCIRegistry) Pusher(ctx context.Context, desc ocispec.Descriptor) (transfer.Pusher, error) {
	var ref = r.reference
	// Annotate ref with digest to push only push tag for single digest
	if !strings.Contains(ref, "@") {
		ref = ref + "@" + desc.Digest.String()
	}
	return r.resolver.Pusher(ctx, ref)
}

func (r *OCIRegistry) MarshalAny(ctx context.Context, sm streaming.StreamCreator) (typeurl.Any, error) {
	res := &transfertypes.RegistryResolver{}
	if r.headers != nil {
		res.Headers = map[string]string{}
		for k := range r.headers {
			res.Headers[k] = r.headers.Get(k)
		}
	}
	if r.creds != nil {
		sid := tstreaming.GenerateID("creds")
		stream, err := sm.Create(ctx, sid)
		if err != nil {
			return nil, err
		}
		go auth.ServeAuthStream(ctx, stream, r.creds.GetCredentials)
		res.AuthStream = sid
	}
	res.HostDir = r.hostDir
	res.DefaultScheme = r.defaultScheme
	s := &transfertypes.OCIRegistry{
		Reference: r.reference,
		Resolver:  res,
	}

	return typeurl.MarshalAny(s)
}

func (r *OCIRegistry) UnmarshalAny(ctx context.Context, sm streaming.StreamGetter, a typeurl.Any) error {
	var s transfertypes.OCIRegistry
	if err := typeurl.UnmarshalTo(a, &s); err != nil {
		return err
	}

	hostOptions := config.HostOptions{}
	if s.Resolver != nil {
		if s.Resolver.HostDir != "" {
			hostOptions.HostDir = config.HostDirFromRoot(s.Resolver.HostDir)
		}
		if s.Resolver.DefaultScheme != "" {
			hostOptions.DefaultScheme = s.Resolver.DefaultScheme
		}
		if sid := s.Resolver.AuthStream; sid != "" {
			stream, err := sm.Get(ctx, sid)
			if err != nil {
				log.G(ctx).WithError(err).WithField("stream", sid).Debug("failed to get auth stream")
				return err
			}
			r.creds = auth.NewCredentialHelper(stream)
			hostOptions.Credentials = func(host string) (string, string, error) {
				c, err := r.creds.GetCredentials(context.Background(), s.Reference, host)
				if err != nil {
					return "", "", err
				}

				return c.Username, c.Secret, nil
			}
		}
		r.headers = http.Header{}
		for k, v := range s.Resolver.Headers {
			r.headers.Add(k, v)
		}
	}

	r.reference = s.Reference
	r.resolver = docker.NewResolver(docker.ResolverOptions{
		Hosts:   config.ConfigureHosts(ctx, hostOptions),
		Headers: r.headers,
	})

	return nil
}
