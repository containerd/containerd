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

package images

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	profilestore "github.com/containerd/containerd/v2/internal/cri/store/securityprofile"
	"github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	crierrors "k8s.io/cri-api/pkg/errors"
)

const seccompConfigMediaType = "application/vnd.cncf.seccomp-profile.config.v1+json"

func invalidProfile(err error) error {
	return fmt.Errorf("%w: %v", crierrors.ErrSecurityProfileInvalid, err)
}

// PullSecurityProfile never registers artifacts with the image service or unpacks them.
func (c *GRPCCRIImageService) PullSecurityProfile(ctx context.Context, req *runtime.PullSecurityProfileRequest) (*runtime.PullSecurityProfileResponse, error) {
	if req.GetProfileKind() != runtime.SecurityProfileKind_Seccomp {
		return nil, status.Error(codes.InvalidArgument, "only seccomp OCI profiles are supported")
	}
	ref := req.GetImage().GetImage()
	requested, err := profilestore.ReferenceDigest(ref)
	if err != nil {
		return nil, invalidProfile(err)
	}
	if _, err := c.resolveRuntimeHandler(req.GetImage().GetRuntimeHandler()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	if c.securityProfiles == nil {
		return nil, status.Error(codes.Unavailable, "security profile store is unavailable")
	}
	cached, err := c.securityProfiles.Remember(ref)
	if err != nil {
		return nil, invalidProfile(err)
	}
	if cached {
		return &runtime.PullSecurityProfileResponse{Cached: true}, nil
	}
	if err := c.securityProfilePulls.Lock(ctx, ref); err != nil {
		return nil, err
	}
	defer c.securityProfilePulls.Unlock(ref)
	cached, err = c.securityProfiles.Remember(ref)
	if err != nil {
		return nil, invalidProfile(err)
	}
	if cached {
		return &runtime.PullSecurityProfileResponse{Cached: true}, nil
	}
	progressTimeout, err := time.ParseDuration(c.config.ImagePullProgressTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image_pull_progress_timeout %q: %w", c.config.ImagePullProgressTimeout, err)
	}
	pullCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if c.downloadLimiter != nil {
		if err := c.downloadLimiter.Acquire(pullCtx, 1); err != nil {
			return nil, err
		}
		defer c.downloadLimiter.Release(1)
	}
	pullReporter := newPullProgressReporter(ref, cancel, progressTimeout)
	pullReporter.start(pullCtx)
	credentials := func(host string) (string, string, error) {
		auth := req.GetAuth()
		if auth == nil {
			if configured := c.config.Registry.Configs[host].Auth; configured != nil {
				auth = toRuntimeAuthConfig(*configured)
			}
		}
		return ParseAuth(auth, host)
	}
	resolver := docker.NewResolver(docker.ResolverOptions{Headers: c.config.Registry.Headers, Hosts: c.registryHosts(pullCtx, credentials, pullReporter.optionUpdateClient)})
	name, desc, err := resolver.Resolve(pullCtx, ref)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", crierrors.ErrRegistryUnavailable, err)
	}
	if desc.Digest != requested {
		return nil, invalidProfile(errors.New("resolved digest differs from requested digest"))
	}
	if desc.MediaType != imagespec.MediaTypeImageManifest {
		return nil, invalidProfile(fmt.Errorf("expected an OCI image manifest, got %q", desc.MediaType))
	}
	for name, verifier := range c.profileVerifiers {
		judgement, err := verifier.VerifyImage(pullCtx, ref, desc)
		if err != nil {
			return nil, fmt.Errorf("%w: verifier %s: %v", crierrors.ErrSignatureValidationFailed, name, err)
		}
		if judgement == nil || !judgement.OK {
			return nil, fmt.Errorf("%w: verifier %s rejected security profile", crierrors.ErrSignatureValidationFailed, name)
		}
	}
	fetcher, err := resolver.Fetcher(pullCtx, name)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", crierrors.ErrRegistryUnavailable, err)
	}
	manifestData, err := fetchProfileBlob(pullCtx, fetcher, desc, profilestore.MaxSize)
	if err != nil {
		return nil, err
	}
	var manifest imagespec.Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, invalidProfile(err)
	}
	if manifest.SchemaVersion != 2 || len(manifest.Layers) != 1 {
		return nil, invalidProfile(errors.New("security profile must have exactly one layer and schemaVersion 2"))
	}
	if manifest.Config.MediaType != seccompConfigMediaType && (manifest.Config.MediaType != imagespec.MediaTypeEmptyJSON || manifest.ArtifactType != seccompConfigMediaType) {
		return nil, invalidProfile(errors.New("artifact kind does not match seccomp config media type"))
	}
	configData, err := fetchProfileBlob(pullCtx, fetcher, manifest.Config, profilestore.MaxSize)
	if err != nil {
		return nil, err
	}
	if manifest.Config.MediaType == imagespec.MediaTypeEmptyJSON && !bytes.Equal(bytes.TrimSpace(configData), []byte("{}")) {
		return nil, invalidProfile(errors.New("OCI empty config must contain an empty JSON object"))
	}
	data, err := fetchProfileBlob(pullCtx, fetcher, manifest.Layers[0], profilestore.MaxSize)
	if err != nil {
		return nil, err
	}
	if _, err := profilestore.Decode(data); err != nil {
		return nil, invalidProfile(err)
	}
	if err := c.securityProfiles.Put(ref, data); err != nil {
		return nil, err
	}
	return &runtime.PullSecurityProfileResponse{}, nil
}

// fetchProfileBlob bounds both advertised and actual size, and checks the digest
// even when a custom resolver or mirror supplies the bytes.
func fetchProfileBlob(ctx context.Context, fetcher remotes.Fetcher, desc imagespec.Descriptor, limit int64) ([]byte, error) {
	// Security profile artifacts are fetched from the registry that resolved the
	// reference. Do not follow manifest-provided URLs to unrelated endpoints.
	desc.URLs = nil
	if desc.Size < 0 || desc.Size > limit {
		return nil, invalidProfile(errors.New("artifact blob exceeds size limit"))
	}
	if err := desc.Digest.Validate(); err != nil {
		return nil, invalidProfile(err)
	}
	r, err := fetcher.Fetch(ctx, desc)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil, invalidProfile(err)
		}
		return nil, fmt.Errorf("%w: %v", crierrors.ErrRegistryUnavailable, err)
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", crierrors.ErrRegistryUnavailable, err)
	}
	if int64(len(data)) != desc.Size || int64(len(data)) > limit {
		return nil, invalidProfile(errors.New("artifact blob size mismatch or limit exceeded"))
	}
	verifier := desc.Digest.Verifier()
	if _, err := verifier.Write(data); err != nil {
		return nil, invalidProfile(err)
	}
	if !verifier.Verified() {
		return nil, invalidProfile(errors.New("artifact blob digest mismatch"))
	}
	return data, nil
}

func (c *GRPCCRIImageService) ListSecurityProfiles(context.Context, *runtime.ListSecurityProfilesRequest) (*runtime.ListSecurityProfilesResponse, error) {
	if c.securityProfiles == nil {
		return nil, status.Error(codes.Unavailable, "security profile store is unavailable")
	}
	profiles, err := c.securityProfiles.List()
	if err != nil {
		return nil, err
	}
	return &runtime.ListSecurityProfilesResponse{Profiles: profiles}, nil
}

func (c *GRPCCRIImageService) RemoveSecurityProfile(_ context.Context, req *runtime.RemoveSecurityProfileRequest) (*runtime.RemoveSecurityProfileResponse, error) {
	if c.securityProfiles == nil {
		return nil, status.Error(codes.Unavailable, "security profile store is unavailable")
	}
	if _, err := digest.Parse(req.GetDigest()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid profile digest: %v", err)
	}
	if err := c.securityProfiles.Remove(req.GetDigest()); err != nil {
		return nil, err
	}
	return &runtime.RemoveSecurityProfileResponse{}, nil
}

// LoadSecurityProfile makes the same store available to sandbox and container spec generation.
func (c *CRIImageService) LoadSecurityProfile(ref string) (*specs.LinuxSeccomp, error) {
	if c.securityProfiles == nil {
		return nil, errors.New("security profile store is unavailable")
	}
	return c.securityProfiles.Get(ref)
}
