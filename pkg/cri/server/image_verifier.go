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
	"github.com/sirupsen/logrus"
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

	rlog := log.G(ctx).WithFields(logrus.Fields{
		"req.name":   name,
		"req.digest": digest,
	})
	rlog.Info("Verifying image ref")

	resp, err := r.verifier.VerifyImage(ctx, &imageverifier.VerifyImageRequest{
		ImageName:   name,
		ImageDigest: digest,
	})
	if err != nil {
		rlog.WithError(err).Error("Failed to verify image, blocking pull")
		return "", ocispec.Descriptor{}, fmt.Errorf("VerifyImage RPC failed: %w", err)
	}

	rlog = rlog.WithFields(logrus.Fields{
		"resp.ok":     resp.Ok,
		"resp.reason": resp.Reason,
	})

	if !resp.Ok {
		rlog.Warn("Image verifier blocked pull")
		return "", ocispec.Descriptor{}, fmt.Errorf("image verifier blocked pull of %v with digest %v for reason: %v", ref, digest, resp.Reason)
	}

	rlog.Info("Image verifier allowed pull")
	return name, desc, err
}

func (r *verifyingResolver) Fetcher(ctx context.Context, ref string) (remotes.Fetcher, error) {
	return r.wrapped.Fetcher(ctx, ref)
}

func (r *verifyingResolver) Pusher(ctx context.Context, ref string) (remotes.Pusher, error) {
	return r.wrapped.Pusher(ctx, ref)
}
