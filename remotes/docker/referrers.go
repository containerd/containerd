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

package docker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func (r dockerFetcher) FetchReferrers(ctx context.Context, dgst digest.Digest, artifactTypes ...string) (io.ReadCloser, ocispec.Descriptor, error) {
	var desc ocispec.Descriptor
	desc.MediaType = ocispec.MediaTypeImageIndex
	ctx = log.WithLogger(ctx, log.G(ctx).WithField("digest", dgst))

	hosts := r.filterHosts(HostCapabilityResolve, HostCapabilityReferrers)
	if len(hosts) == 0 {
		return nil, desc, fmt.Errorf("no pull hosts: %w", errdefs.ErrNotFound)
	}

	ctx, err := ContextWithRepositoryScope(ctx, r.refspec, false)
	if err != nil {
		return nil, desc, err
	}

	for _, host := range hosts {
		var req *request
		if host.Capabilities.Has(HostCapabilityReferrers) {
			req = r.request(host, http.MethodGet, "referrers", dgst.String())
			for _, artifactType := range artifactTypes {
				if err := req.addQuery("artifactType", artifactType); err != nil {
					return nil, desc, err
				}
			}
			if err := req.addNamespace(r.refspec.Hostname()); err != nil {
				return nil, desc, err
			}

			rc, cl, err := r.open(ctx, req, desc.MediaType, 0)
			if err != nil {
				if !errdefs.IsNotFound(err) {
					return nil, desc, err
				}
			} else {
				desc.Size = cl
				// Digest is not known ahead of time and there is nothing in the distribution
				// specification defining an HTTP header to return the digest on referrers.
				return rc, desc, nil
			}
		}
		if host.Capabilities.Has(HostCapabilityResolve) {
			req = r.request(host, http.MethodGet, "manifests", strings.Replace(dgst.String(), ":", "-", 1))
			if err := req.addNamespace(r.refspec.Hostname()); err != nil {
				return nil, desc, err
			}
			rc, cl, err := r.open(ctx, req, desc.MediaType, 0)
			if err != nil {
				if !errdefs.IsNotFound(err) {
					return nil, desc, err
				}
			} else {
				desc.Size = cl
				// Digest could be resolved here the same as for any manifest, don't include the
				// digest for consistency with the referrers endpoint.
				return rc, desc, nil
			}
		}
	}

	return nil, ocispec.Descriptor{}, fmt.Errorf("could not be found at any host: %w", errdefs.ErrNotFound)
}
