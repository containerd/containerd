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

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"

	"github.com/containerd/containerd/content"
	containerdimages "github.com/containerd/containerd/images"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/api/v1alpha1/runtime"
)

// PullImage pulls an image with authentication config.
// TODO(mikebrow): add authentication
// TODO(mikebrow): harden api (including figuring out at what layer we should be blocking on duplicate requests.)
func (c *criContainerdService) PullImage(ctx context.Context, r *runtime.PullImageRequest) (*runtime.PullImageResponse, error) {
	var (
		err  error
		size int64
		desc imagespec.Descriptor
	)

	image := r.GetImage().Image

	if desc, size, err = c.pullImage(ctx, image); err != nil {
		return nil, fmt.Errorf("failed to pull image %q: %v", image, err)
	}
	digest := desc.Digest.String() // TODO(mikebrow): add truncIndex for image id

	// TODO(mikebrow): pass a metadata struct to pullimage and fill in the tags/digests
	// store the image metadata
	// TODO(mikebrow): consider what to do if pullimage was called and metadata already exists (error? udpate?)
	meta := &metadata.ImageMetadata{
		ID:          digest,
		RepoTags:    []string{image},
		RepoDigests: []string{digest},
		Size:        uint64(size), // TODO(mikebrow):  compressed or uncompressed size? using compressed
	}
	if err = c.imageMetadataStore.Create(*meta); err != nil {
		return &runtime.PullImageResponse{ImageRef: digest},
			fmt.Errorf("pulled image `%q` but failed to store metadata for digest: %s err: %v", image, digest, err)
	}

	// Return the image digest
	return &runtime.PullImageResponse{ImageRef: digest}, err
}

// imageReferenceResolver attempts to resolve the image reference into a name
// and manifest via the containerd library call..
//
// The argument `ref` should be a scheme-less URI representing the remote.
// Structurally, it has a host and path. The "host" can be used to directly
// reference a specific host or be matched against a specific handler.
//
// The returned name should be used to identify the referenced entity.
// Dependending on the remote namespace, this may be immutable or mutable.
// While the name may differ from ref, it should itself be a valid ref.
//
// If the resolution fails, an error will be returned.
// TODO(mikebrow) add config.json based image.Config as an additional return value from this resolver fn()
func (c *criContainerdService) imageReferenceResolver(ctx context.Context, ref string) (resolvedImageName string, manifest imagespec.Manifest, compressedSize uint64, err error) {
	var (
		size    int64
		desc    imagespec.Descriptor
		fetcher remotes.Fetcher
	)

	// Resolve the image name; place that in the image store; then dispatch
	// a handler to fetch the object for the manifest
	resolver := docker.NewResolver()
	resolvedImageName, desc, fetcher, err = resolver.Resolve(ctx, ref)
	if err != nil {
		return resolvedImageName, manifest, compressedSize, fmt.Errorf("failed to resolve ref %q: err: %v", ref, err)
	}

	err = c.imageStore.Put(ctx, resolvedImageName, desc)
	if err != nil {
		return resolvedImageName, manifest, compressedSize, fmt.Errorf("failed to put %q: desc: %v err: %v", resolvedImageName, desc, err)
	}

	err = containerdimages.Dispatch(
		ctx,
		remotes.FetchHandler(c.contentIngester, fetcher),
		desc)
	if err != nil {
		return resolvedImageName, manifest, compressedSize, fmt.Errorf("failed to fetch %q: desc: %v err: %v", resolvedImageName, desc, err)
	}

	image, err := c.imageStore.Get(ctx, resolvedImageName)
	if err != nil {
		return resolvedImageName, manifest, compressedSize,
			fmt.Errorf("get failed for image:%q err: %v", resolvedImageName, err)
	}
	p, err := content.ReadBlob(ctx, c.contentProvider, image.Target.Digest)
	if err != nil {
		return resolvedImageName, manifest, compressedSize,
			fmt.Errorf("readblob failed for digest:%q err: %v", image.Target.Digest, err)
	}
	err = json.Unmarshal(p, &manifest)
	if err != nil {
		return resolvedImageName, manifest, compressedSize,
			fmt.Errorf("unmarshal blob to manifest failed for digest:%q err: %v", image.Target.Digest, err)
	}
	size, err = image.Size(ctx, c.contentProvider)
	if err != nil {
		return resolvedImageName, manifest, compressedSize,
			fmt.Errorf("size failed for image:%q %v", image.Target.Digest, err)
	}
	compressedSize = uint64(size)
	return resolvedImageName, manifest, compressedSize, nil
}

func (c *criContainerdService) pullImage(ctx context.Context, ref string) (imagespec.Descriptor, int64, error) {
	var (
		err               error
		size              int64
		desc              imagespec.Descriptor
		resolvedImageName string
		fetcher           remotes.Fetcher
	)

	// Resolve the image name; place that in the image store; then dispatch
	// a handler for a sequence of handlers which: 1) fetch the object using a
	// FetchHandler; and 3) recurse through any sub-layers via a ChildrenHandler
	resolver := docker.NewResolver()

	resolvedImageName, desc, fetcher, err = resolver.Resolve(ctx, ref)
	if err != nil {
		return desc, size, fmt.Errorf("failed to resolve ref %q: err: %v", ref, err)
	}

	err = c.imageStore.Put(ctx, resolvedImageName, desc)
	if err != nil {
		return desc, size, fmt.Errorf("failed to put %q: desc: %v err: %v", resolvedImageName, desc, err)
	}

	err = containerdimages.Dispatch(
		ctx,
		containerdimages.Handlers(
			remotes.FetchHandler(c.contentIngester, fetcher),
			containerdimages.ChildrenHandler(c.contentProvider)),
		desc)
	if err != nil {
		return desc, size, fmt.Errorf("failed to fetch %q: desc: %v err: %v", resolvedImageName, desc, err)
	}

	image, err := c.imageStore.Get(ctx, resolvedImageName)
	if err != nil {
		return desc, size,
			fmt.Errorf("get failed for image:%q err: %v", resolvedImageName, err)
	}
	p, err := content.ReadBlob(ctx, c.contentProvider, image.Target.Digest)
	if err != nil {
		return desc, size,
			fmt.Errorf("readblob failed for digest:%q err: %v", image.Target.Digest, err)
	}
	var manifest imagespec.Manifest
	err = json.Unmarshal(p, &manifest)
	if err != nil {
		return desc, size,
			fmt.Errorf("unmarshal blob to manifest failed for digest:%q %v", image.Target.Digest, err)
	}
	_, err = c.rootfsUnpacker.Unpack(ctx, manifest.Layers) // ignoring returned chainID for now
	if err != nil {
		return desc, size,
			fmt.Errorf("unpack failed for manifest layers:%v %v", manifest.Layers, err)
	}
	size, err = image.Size(ctx, c.contentProvider)
	if err != nil {
		return desc, size,
			fmt.Errorf("size failed for image:%q %v", image.Target.Digest, err)
	}
	return desc, size, nil
}
