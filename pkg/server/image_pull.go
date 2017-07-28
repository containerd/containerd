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
	gocontext "context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/content"
	containerdimages "github.com/containerd/containerd/images"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/containerd/containerd/remotes/docker/schema1"
	containerdrootfs "github.com/containerd/containerd/rootfs"
	"github.com/golang/glog"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	imagestore "github.com/kubernetes-incubator/cri-containerd/pkg/store/image"
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
func (c *criContainerdService) PullImage(ctx context.Context, r *runtime.PullImageRequest) (retRes *runtime.PullImageResponse, retErr error) {
	glog.V(2).Infof("PullImage %q with auth config %+v", r.GetImage().GetImage(), r.GetAuth())
	defer func() {
		if retErr == nil {
			glog.V(2).Infof("PullImage %q returns image reference %q",
				r.GetImage().GetImage(), retRes.GetImageRef())
		}
	}()
	imageRef := r.GetImage().GetImage()

	// TODO(mikebrow): add truncIndex for image id
	imageID, repoTag, repoDigest, err := c.pullImage(ctx, imageRef, r.GetAuth())
	if err != nil {
		return nil, fmt.Errorf("failed to pull image %q: %v", imageRef, err)
	}
	glog.V(4).Infof("Pulled image %q with image id %q, repo tag %q, repo digest %q", imageRef, imageID,
		repoTag, repoDigest)

	// Get image information.
	chainID, size, config, err := c.getImageInfo(ctx, imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to get image %q information: %v", imageRef, err)
	}
	image := imagestore.Image{
		ID:      imageID,
		ChainID: chainID.String(),
		Size:    size,
		Config:  config,
	}

	if repoDigest != "" {
		image.RepoDigests = []string{repoDigest}
	}
	if repoTag != "" {
		image.RepoTags = []string{repoTag}
	}
	c.imageStore.Add(image)

	// NOTE(random-liu): the actual state in containerd is the source of truth, even we maintain
	// in-memory image store, it's only for in-memory indexing. The image could be removed
	// by someone else anytime, before/during/after we create the metadata. We should always
	// check the actual state in containerd before using the image or returning status of the
	// image.
	return &runtime.PullImageResponse{ImageRef: imageID}, err
}

// resourceSet is the helper struct to help tracking all resources associated
// with an image.
type resourceSet struct {
	sync.Mutex
	resources map[string]struct{}
}

func newResourceSet() *resourceSet {
	return &resourceSet{resources: make(map[string]struct{})}
}

func (r *resourceSet) add(resource string) {
	r.Lock()
	defer r.Unlock()
	r.resources[resource] = struct{}{}
}

// all returns an array of all resources added.
func (r *resourceSet) all() map[string]struct{} {
	r.Lock()
	defer r.Unlock()
	resources := make(map[string]struct{})
	for resource := range r.resources {
		resources[resource] = struct{}{}
	}
	return resources
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

// pullImage pulls image and returns image id (config digest), repoTag and repoDigest.
func (c *criContainerdService) pullImage(ctx context.Context, rawRef string, auth *runtime.AuthConfig) (
	// TODO(random-liu): Replace with client.Pull.
	string, string, string, error) {
	namedRef, err := normalizeImageRef(rawRef)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to parse image reference %q: %v", rawRef, err)
	}
	// TODO(random-liu): [P0] Avoid concurrent pulling/removing on the same image reference.
	ref := namedRef.String()
	if ref != rawRef {
		glog.V(4).Infof("PullImage using normalized image ref: %q", ref)
	}

	// Resolve the image reference to get descriptor and fetcher.
	resolver := docker.NewResolver(docker.ResolverOptions{
		Credentials: func(string) (string, string, error) { return ParseAuth(auth) },
		Client:      http.DefaultClient,
	})
	_, desc, err := resolver.Resolve(ctx, ref)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to resolve ref %q: %v", ref, err)
	}
	fetcher, err := resolver.Fetcher(ctx, ref)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get fetcher for ref %q: %v", ref, err)
	}
	// Currently, the resolved image name is the same with ref in docker resolver,
	// but they may be different in the future.
	// TODO(random-liu): Always resolve image reference and use resolved image name in
	// the system.

	glog.V(4).Infof("Start downloading resources for image %q", ref)
	resources := newResourceSet()
	resourceTrackHandler := containerdimages.HandlerFunc(func(ctx gocontext.Context, desc imagespec.Descriptor) (
		[]imagespec.Descriptor, error) {
		resources.add(remotes.MakeRefKey(ctx, desc))
		return nil, nil
	})
	// Fetch all image resources into content store.
	// Dispatch a handler which will run a sequence of handlers to:
	// 1) track all resources associated using a customized handler;
	// 2) fetch the object using a FetchHandler;
	// 3) recurse through any sub-layers via a ChildrenHandler.
	// Support schema1 image.
	var (
		schema1Converter *schema1.Converter
		handler          containerdimages.Handler
	)
	if desc.MediaType == containerdimages.MediaTypeDockerSchema1Manifest {
		schema1Converter = schema1.NewConverter(c.contentStoreService, fetcher)
		handler = containerdimages.Handlers(
			resourceTrackHandler,
			schema1Converter,
		)
	} else {
		handler = containerdimages.Handlers(
			resourceTrackHandler,
			remotes.FetchHandler(c.contentStoreService, fetcher),
			containerdimages.ChildrenHandler(c.contentStoreService),
		)
	}
	if err := containerdimages.Dispatch(ctx, handler, desc); err != nil {
		// Dispatch returns error when requested resources are locked.
		// In that case, we should start waiting and checking the pulling
		// progress.
		// TODO(random-liu): Check specific resource locked error type.
		glog.V(5).Infof("Dispatch for %q returns error: %v", ref, err)
	}
	// Wait for the image pulling to finish
	if err := c.waitForResourcesDownloading(ctx, resources.all()); err != nil {
		return "", "", "", fmt.Errorf("failed to wait for image %q downloading: %v", ref, err)
	}
	glog.V(4).Infof("Finished downloading resources for image %q", ref)
	if schema1Converter != nil {
		desc, err = schema1Converter.Convert(ctx)
		if err != nil {
			return "", "", "", fmt.Errorf("failed to convert schema 1 image %q: %v", ref, err)
		}
	}

	// In the future, containerd will rely on the information in the image store to perform image
	// garbage collection.
	// For now, we simply use it to store and retrieve information required for pulling an image.
	// @stevvooe said we should `Put` before downloading content, However:
	// 1) Containerd client put image metadata after downloading;
	// 2) We need desc returned by schema1 converter.
	// So just put the image metadata after downloading now.
	// TODO(random-liu): Fix the potential garbage collection race.
	repoDigest, repoTag := getRepoDigestAndTag(namedRef, desc.Digest, schema1Converter != nil)
	if ref != repoTag && ref != repoDigest {
		return "", "", "", fmt.Errorf("unexpected repo tag %q and repo digest %q for %q", repoTag, repoDigest, ref)
	}
	for _, r := range []string{repoTag, repoDigest} {
		if r == "" {
			continue
		}
		if err := c.imageStoreService.Put(ctx, r, desc); err != nil {
			return "", "", "", fmt.Errorf("failed to put image reference %q desc %v into containerd image store: %v",
				r, desc, err)
		}
	}
	// Do not cleanup if following operations fail so as to make resumable download possible.
	// TODO(random-liu): Replace with image.Unpack.
	// Unpack the image layers into snapshots.
	image, err := c.imageStoreService.Get(ctx, ref)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get image %q from containerd image store: %v", ref, err)
	}
	// Read the image manifest from content store.
	manifestDigest := image.Target.Digest
	p, err := content.ReadBlob(ctx, c.contentStoreService, manifestDigest)
	if err != nil {
		return "", "", "", fmt.Errorf("readblob failed for manifest digest %q: %v", manifestDigest, err)
	}
	var manifest imagespec.Manifest
	if err := json.Unmarshal(p, &manifest); err != nil {
		return "", "", "", fmt.Errorf("unmarshal blob to manifest failed for manifest digest %q: %v",
			manifestDigest, err)
	}
	diffIDs, err := image.RootFS(ctx, c.contentStoreService)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get image rootfs: %v", err)
	}
	if len(diffIDs) != len(manifest.Layers) {
		return "", "", "", fmt.Errorf("mismatched image rootfs and manifest layers")
	}
	layers := make([]containerdrootfs.Layer, len(diffIDs))
	for i := range diffIDs {
		layers[i].Diff = imagespec.Descriptor{
			// TODO: derive media type from compressed type
			MediaType: imagespec.MediaTypeImageLayer,
			Digest:    diffIDs[i],
		}
		layers[i].Blob = manifest.Layers[i]
	}
	if _, err := containerdrootfs.ApplyLayers(ctx, layers, c.snapshotService, c.diffService); err != nil {
		return "", "", "", fmt.Errorf("failed to apply layers %+v: %v", layers, err)
	}

	// TODO(random-liu): Considering how to deal with the disk usage of content.

	configDesc, err := image.Config(ctx, c.contentStoreService)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get config descriptor for image %q: %v", ref, err)
	}
	// Use config digest as imageID to conform to oci image spec, and also add image id as
	// image reference.
	imageID := configDesc.Digest.String()
	if err := c.imageStoreService.Put(ctx, imageID, desc); err != nil {
		return "", "", "", fmt.Errorf("failed to put image id %q into containerd image store: %v",
			imageID, err)
	}
	return imageID, repoTag, repoDigest, nil
}

// waitDownloadingPollInterval is the interval to check resource downloading progress.
const waitDownloadingPollInterval = 200 * time.Millisecond

// waitForResourcesDownloading waits for all resource downloading to finish.
func (c *criContainerdService) waitForResourcesDownloading(ctx context.Context, resources map[string]struct{}) error {
	ticker := time.NewTicker(waitDownloadingPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			// TODO(random-liu): Use better regexp when containerd `MakeRefKey` contains more
			// information.
			statuses, err := c.contentStoreService.Status(ctx, "")
			if err != nil {
				return fmt.Errorf("failed to get content status: %v", err)
			}
			pulling := false
			// TODO(random-liu): Move Dispatch into a separate goroutine, so that we could report
			// image pulling progress concurrently.
			for _, status := range statuses {
				_, ok := resources[status.Ref]
				if ok {
					glog.V(5).Infof("Pulling resource %q with progress %d/%d",
						status.Ref, status.Offset, status.Total)
					pulling = true
				}
			}
			if !pulling {
				return nil
			}
		case <-ctx.Done():
			// TODO(random-liu): Abort ongoing pulling if cancelled.
			return fmt.Errorf("image resources pulling is cancelled")
		}
	}
}
