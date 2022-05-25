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
	"fmt"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/cri/server/imageverifier/v1"
	"github.com/containerd/containerd/remotes"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type verifyingResolver struct {
	wrapped  remotes.Resolver
	verifier imageverifier.ImageVerifierService
}

func NewVerifyingResolver(wrapped remotes.Resolver, verifier imageverifier.ImageVerifierService) remotes.Resolver {
	return &verifyingResolver{
		wrapped:  wrapped,
		verifier: verifier,
	}
}

func (r *verifyingResolver) Resolve(ctx context.Context, ref string) (name string, desc ocispec.Descriptor, err error) {
	name, desc, err = r.wrapped.Resolve(ctx, ref)
	if err != nil {
		return "", ocispec.Descriptor{}, err
	}

	digest := desc.Digest.String()

	log.G(ctx).Infof("Verifying image ref: %q, name: %q, digest %q", ref, name, digest)
	resp, err := r.verifier.VerifyImage(ctx, &imageverifier.VerifyImageRequest{
		ImageName:   name,
		ImageDigest: digest,
	})
	if err != nil {
		return "", ocispec.Descriptor{}, fmt.Errorf("VerifyImage RPC failed: %w", err)
	}
	log.G(ctx).Infof("Image verifier plugin returned OK: %t with reason: %q", resp.Ok, resp.Reason)

	if !resp.Ok {
		return "", ocispec.Descriptor{}, fmt.Errorf("image verifier blocked pull of %q with reason %q", ref, resp.Reason)
	}

	return name, desc, err
}

func (r *verifyingResolver) Fetcher(ctx context.Context, ref string) (remotes.Fetcher, error) {
	return r.wrapped.Fetcher(ctx, ref)
}

func (r *verifyingResolver) Pusher(ctx context.Context, ref string) (remotes.Pusher, error) {
	return r.wrapped.Pusher(ctx, ref)
}
