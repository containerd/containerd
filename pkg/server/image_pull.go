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
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/containerd/containerd/content"
	containerdimages "github.com/containerd/containerd/images"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/golang/glog"
	imagedigest "github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
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
// 2) Containerd suggests user to add entry before pulling the image. However if
// an error occurrs during the pulling, should we remove the entry from metadata
// store? Or should we leave it there until next startup (resource leakage)?
//
// 3) CRI-containerd only exposes "READY" (successfully pulled and unpacked) images
// to the user, which are maintained in the in-memory metadata index. However, it's
// still possible that someone else removes the content or snapshot by-pass cri-containerd,
// how do we detect that and update the in-memory metadata correspondingly? Always
// check whether corresponding snapshot is ready when reporting image status?
//
// 4) Is the content important if we cached necessary information in-memory
// after we pull the image? How to manage the disk usage of contents? If some
// contents are missing but snapshots are ready, is the image still "READY"?

// PullImage pulls an image with authentication config.
// TODO(mikebrow): harden api (including figuring out at what layer we should be blocking on duplicate requests.)
func (c *criContainerdService) PullImage(ctx context.Context, r *runtime.PullImageRequest) (retRes *runtime.PullImageResponse, retErr error) {
	glog.V(2).Infof("PullImage %q with auth config %+v", r.GetImage().GetImage(), r.GetAuth())
	defer func() {
		if retErr == nil {
			glog.V(2).Infof("PullImage %q returns image reference %q",
				r.GetImage().GetImage(), retRes.GetImageRef())
		}
	}()

	namedRef, err := normalizeImageRef(r.GetImage().GetImage())
	if err != nil {
		return nil, fmt.Errorf("failed to parse image reference %q: %v", r.GetImage().GetImage(), err)
	}
	// TODO(random-liu): [P0] Avoid concurrent pulling/removing on the same image reference.
	image := namedRef.String()
	if r.GetImage().GetImage() != image {
		glog.V(4).Infof("PullImage using normalized image ref: %q", image)
	}

	// TODO(random-liu): [P1] Schema 1 image is not supported in containerd now, we need to support
	// it for backward compatiblity.
	cfgDigest, manifestDigest, err := c.pullImage(ctx, image)
	if err != nil {
		return nil, fmt.Errorf("failed to pull image %q: %v", image, err)
	}
	// Use config digest as imageID to conform to oci image spec.
	// TODO(mikebrow): add truncIndex for image id
	imageID := cfgDigest.String()
	glog.V(4).Infof("Pulled image %q with image id %q, manifest digest %q", image, imageID, manifestDigest)

	repoDigest, repoTag := getRepoDigestAndTag(namedRef, manifestDigest)
	_, err = c.imageMetadataStore.Get(imageID)
	if err != nil && !metadata.IsNotExistError(err) {
		return nil, fmt.Errorf("failed to get image %q metadata: %v", imageID, err)
	}
	// There is a known race here because the image metadata could be created after `Get`.
	// TODO(random-liu): [P1] Do not use metadata store. Use simple in-memory data structure to
	// maintain the id -> information index. And use the container image store as backup and
	// recover in-memory state during startup.
	if err == nil {
		// Update existing image metadata.
		if err := c.imageMetadataStore.Update(imageID, func(m metadata.ImageMetadata) (metadata.ImageMetadata, error) {
			updateImageMetadata(&m, repoTag, repoDigest)
			return m, nil
		}); err != nil {
			return nil, fmt.Errorf("failed to update image %q metadata: %v", imageID, err)
		}
		return &runtime.PullImageResponse{ImageRef: imageID}, err
	}

	// Get image information.
	chainID, size, config, err := c.getImageInfo(ctx, image)
	if err != nil {
		return nil, fmt.Errorf("failed to get image %q information: %v", image, err)
	}

	// NOTE(random-liu): the actual state in containerd is the source of truth, even we maintain
	// in-memory image metadata, it's only for in-memory indexing. The image could be removed
	// by someone else anytime, before/during/after we create the metadata. We should always
	// check the actual state in containerd before using the image or returning status of the
	// image.

	// Create corresponding image metadata.
	newMeta := metadata.ImageMetadata{
		ID:      imageID,
		ChainID: chainID.String(),
		Size:    size,
		Config:  config,
	}
	// Add the image reference used into repo tags. Note if the image is pulled with
	// repo digest, it will also be added in to repo tags, which is fine.
	updateImageMetadata(&newMeta, repoTag, repoDigest)
	if err = c.imageMetadataStore.Create(newMeta); err != nil {
		return nil, fmt.Errorf("failed to create image %q metadata: %v", imageID, err)
	}
	return &runtime.PullImageResponse{ImageRef: imageID}, err
}

// pullImage pulls image and returns image id (config digest) and manifest digest.
// The ref should be normalized image reference.
// TODO(random-liu): [P0] Wait for all downloadings to be done before return.
func (c *criContainerdService) pullImage(ctx context.Context, ref string) (
	imagedigest.Digest, imagedigest.Digest, error) {
	// Resolve the image reference to get descriptor and fetcher.
	resolver := docker.NewResolver(docker.ResolverOptions{
		// TODO(random-liu): Add authentication by setting credentials.
		// TODO(random-liu): Handle https.
		PlainHTTP: true,
		Client:    http.DefaultClient,
	})
	_, desc, fetcher, err := resolver.Resolve(ctx, ref)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve ref %q: %v", ref, err)
	}
	// Currently, the resolved image name is the same with ref in docker resolver,
	// but they may be different in the future.
	// TODO(random-liu): Always resolve image reference and use resolved image name in
	// the system.

	// Put the image information into containerd image store.
	// In the future, containerd will rely on the information in the image store to perform image
	// garbage collection.
	// For now, we simply use it to store and retrieve information required for pulling an image.
	if putErr := c.imageStoreService.Put(ctx, ref, desc); putErr != nil {
		return "", "", fmt.Errorf("failed to put image %q desc %v into containerd image store: %v",
			ref, desc, putErr)
	}
	// TODO(random-liu): What if following operations fail? Do we need to do cleanup?

	// Fetch all image resources into content store.
	// Dispatch a handler which will run a sequence of handlers to:
	// 1) fetch the object using a FetchHandler;
	// 2) recurse through any sub-layers via a ChildrenHandler.
	err = containerdimages.Dispatch(
		ctx,
		containerdimages.Handlers(
			remotes.FetchHandler(c.contentStoreService, fetcher),
			containerdimages.ChildrenHandler(c.contentStoreService)),
		desc)
	if err != nil {
		return "", "", fmt.Errorf("failed to fetch image %q desc %+v: %v", ref, desc, err)
	}

	image, err := c.imageStoreService.Get(ctx, ref)
	if err != nil {
		return "", "", fmt.Errorf("failed to get image %q from containerd image store: %v", ref, err)
	}
	// Read the image manifest from content store.
	manifestDigest := image.Target.Digest
	p, err := content.ReadBlob(ctx, c.contentStoreService, manifestDigest)
	if err != nil {
		return "", "", fmt.Errorf("readblob failed for manifest digest %q: %v", manifestDigest, err)
	}
	var manifest imagespec.Manifest
	if err := json.Unmarshal(p, &manifest); err != nil {
		return "", "", fmt.Errorf("unmarshal blob to manifest failed for manifest digest %q: %v",
			manifestDigest, err)
	}

	// Unpack the image layers into snapshots.
	if _, err = c.rootfsUnpacker.Unpack(ctx, manifest.Layers); err != nil {
		return "", "", fmt.Errorf("unpack failed for manifest layers %+v: %v", manifest.Layers, err)
	}
	// TODO(random-liu): Considering how to deal with the disk usage of content.

	configDesc, err := image.Config(ctx, c.contentStoreService)
	if err != nil {
		return "", "", fmt.Errorf("failed to get config descriptor for image %q: %v", ref, err)
	}
	return configDesc.Digest, manifestDigest, nil
}

// insertToStringSlice is a helper function to insert a string into the string slice
// if the string is not in the slice yet.
func insertToStringSlice(ss []string, s string) []string {
	found := false
	for _, str := range ss {
		if s == str {
			found = true
			break
		}
	}
	if !found {
		ss = append(ss, s)
	}
	return ss
}

// updateImageMetadata updates existing image meta with new repoTag and repoDigest.
func updateImageMetadata(meta *metadata.ImageMetadata, repoTag, repoDigest string) {
	if repoTag != "" {
		meta.RepoTags = insertToStringSlice(meta.RepoTags, repoTag)
	}
	if repoDigest != "" {
		meta.RepoDigests = insertToStringSlice(meta.RepoDigests, repoDigest)
	}
}
