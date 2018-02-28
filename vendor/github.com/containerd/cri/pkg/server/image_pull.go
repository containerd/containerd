/*
Copyright 2017 The Kubernetes Authors.

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
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/errdefs"
	containerdimages "github.com/containerd/containerd/images"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"

	containerdresolver "github.com/containerd/cri/pkg/containerd/resolver"
	imagestore "github.com/containerd/cri/pkg/store/image"
	"github.com/containerd/cri/pkg/util"
)

// For image management:
// 1) We have an in-memory metadata index to:
//   a. Maintain ImageID -> RepoTags, ImageID -> RepoDigset relationships; ImageID
//   is the digest of image config, which conforms to oci image spec.
//   b. Cache constant and useful information such as image chainID, config etc.
//   c. An image will be added into the in-memory metadata only when it's successfully
//   pulled and unpacked.
//
// 2) We use containerd image metadata store and content store:
//   a. To resolve image reference (digest/tag) locally. During pulling image, we
//   normalize the image reference provided by user, and put it into image metadata
//   store with resolved descriptor. For the other operations, if image id is provided,
//   we'll access the in-memory metadata index directly; if image reference is
//   provided, we'll normalize it, resolve it in containerd image metadata store
//   to get the image id.
//   b. As the backup of in-memory metadata in 1). During startup, the in-memory
//   metadata could be re-constructed from image metadata store + content store.
//
// Several problems with current approach:
// 1) An entry in containerd image metadata store doesn't mean a "READY" (successfully
// pulled and unpacked) image. E.g. during pulling, the client gets killed. In that case,
// if we saw an image without snapshots or with in-complete contents during startup,
// should we re-pull the image? Or should we remove the entry?
//
// yanxuean: We cann't delete image directly, because we don't know if the image
// is pulled by us. There are resource leakage.
//
// 2) Containerd suggests user to add entry before pulling the image. However if
// an error occurrs during the pulling, should we remove the entry from metadata
// store? Or should we leave it there until next startup (resource leakage)?
//
// 3) The cri plugin only exposes "READY" (successfully pulled and unpacked) images
// to the user, which are maintained in the in-memory metadata index. However, it's
// still possible that someone else removes the content or snapshot by-pass the cri plugin,
// how do we detect that and update the in-memory metadata correspondingly? Always
// check whether corresponding snapshot is ready when reporting image status?
//
// 4) Is the content important if we cached necessary information in-memory
// after we pull the image? How to manage the disk usage of contents? If some
// contents are missing but snapshots are ready, is the image still "READY"?

// PullImage pulls an image with authentication config.
func (c *criContainerdService) PullImage(ctx context.Context, r *runtime.PullImageRequest) (*runtime.PullImageResponse, error) {
	imageRef := r.GetImage().GetImage()
	namedRef, err := util.NormalizeImageRef(imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image reference %q: %v", imageRef, err)
	}
	ref := namedRef.String()
	if ref != imageRef {
		logrus.Debugf("PullImage using normalized image ref: %q", ref)
	}
	resolver := containerdresolver.NewResolver(containerdresolver.Options{
		Credentials: func(string) (string, string, error) { return ParseAuth(r.GetAuth()) },
		Client:      http.DefaultClient,
		Registry:    c.getResolverOptions(),
	})
	_, desc, err := resolver.Resolve(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve image %q: %v", ref, err)
	}
	// We have to check schema1 here, because after `Pull`, schema1
	// image has already been converted.
	isSchema1 := desc.MediaType == containerdimages.MediaTypeDockerSchema1Manifest

	// TODO(mikebrow): add truncIndex for image id
	image, err := c.client.Pull(ctx, ref,
		containerd.WithSchema1Conversion,
		containerd.WithResolver(resolver),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to pull image %q: %v", ref, err)
	}

	// Do best effort unpack.
	logrus.Debugf("Unpack image %q", imageRef)
	if err := image.Unpack(ctx, c.config.ContainerdConfig.Snapshotter); err != nil {
		logrus.WithError(err).Warnf("Failed to unpack image %q", imageRef)
		// Do not fail image pulling. Unpack will be retried before container creation.
	}

	// Get image information.
	info, err := getImageInfo(ctx, image)
	if err != nil {
		return nil, fmt.Errorf("failed to get image information: %v", err)
	}
	imageID := info.id

	repoDigest, repoTag := getRepoDigestAndTag(namedRef, image.Target().Digest, isSchema1)
	for _, r := range []string{repoTag, repoDigest, imageID} {
		if r == "" {
			continue
		}
		if err := c.createImageReference(ctx, r, image.Target()); err != nil {
			return nil, fmt.Errorf("failed to update image reference %q: %v", r, err)
		}
	}

	logrus.Debugf("Pulled image %q with image id %q, repo tag %q, repo digest %q", imageRef, imageID,
		repoTag, repoDigest)
	img := imagestore.Image{
		ID:        imageID,
		ChainID:   info.chainID.String(),
		Size:      info.size,
		ImageSpec: info.imagespec,
		Image:     image,
	}
	if repoDigest != "" {
		img.RepoDigests = []string{repoDigest}
	}
	if repoTag != "" {
		img.RepoTags = []string{repoTag}
	}

	if err := c.imageStore.Add(img); err != nil {
		return nil, fmt.Errorf("failed to add image %q into store: %v", img.ID, err)
	}

	// NOTE(random-liu): the actual state in containerd is the source of truth, even we maintain
	// in-memory image store, it's only for in-memory indexing. The image could be removed
	// by someone else anytime, before/during/after we create the metadata. We should always
	// check the actual state in containerd before using the image or returning status of the
	// image.
	return &runtime.PullImageResponse{ImageRef: img.ID}, nil
}

// ParseAuth parses AuthConfig and returns username and password/secret required by containerd.
func ParseAuth(auth *runtime.AuthConfig) (string, string, error) {
	if auth == nil {
		return "", "", nil
	}
	if auth.Username != "" {
		return auth.Username, auth.Password, nil
	}
	if auth.IdentityToken != "" {
		return "", auth.IdentityToken, nil
	}
	if auth.Auth != "" {
		decLen := base64.StdEncoding.DecodedLen(len(auth.Auth))
		decoded := make([]byte, decLen)
		_, err := base64.StdEncoding.Decode(decoded, []byte(auth.Auth))
		if err != nil {
			return "", "", err
		}
		fields := strings.SplitN(string(decoded), ":", 2)
		if len(fields) != 2 {
			return "", "", fmt.Errorf("invalid decoded auth: %q", decoded)
		}
		user, passwd := fields[0], fields[1]
		return user, strings.Trim(passwd, "\x00"), nil
	}
	// TODO(random-liu): Support RegistryToken.
	return "", "", fmt.Errorf("invalid auth config")
}

// createImageReference creates image reference inside containerd image store.
// Note that because create and update are not finished in one transaction, there could be race. E.g.
// the image reference is deleted by someone else after create returns already exists, but before update
// happens.
func (c *criContainerdService) createImageReference(ctx context.Context, name string, desc imagespec.Descriptor) error {
	img := containerdimages.Image{
		Name:   name,
		Target: desc,
	}
	// TODO(random-liu): Figure out which is the more performant sequence create then update or
	// update then create.
	_, err := c.client.ImageService().Create(ctx, img)
	if err == nil {
		return nil
	}
	if err != nil && !errdefs.IsAlreadyExists(err) {
		return err
	}
	_, err = c.client.ImageService().Update(ctx, img, "target")
	return err
}

func (c *criContainerdService) getResolverOptions() map[string][]string {
	options := make(map[string][]string)
	for ns, mirror := range c.config.Mirrors {
		options[ns] = append(options[ns], mirror.Endpoints...)
	}
	return options
}
