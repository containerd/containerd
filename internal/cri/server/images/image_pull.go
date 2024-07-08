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
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/imgcrypt/v2"
	"github.com/containerd/imgcrypt/v2/images/encryption"
	"github.com/containerd/log"
	"github.com/containerd/platforms"
	distribution "github.com/distribution/reference"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/images"
	containerdimages "github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/containerd/containerd/v2/core/remotes/docker/config"
	"github.com/containerd/containerd/v2/core/transfer"
	transferimage "github.com/containerd/containerd/v2/core/transfer/image"
	"github.com/containerd/containerd/v2/core/transfer/registry"
	"github.com/containerd/containerd/v2/internal/cri/annotations"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	crilabels "github.com/containerd/containerd/v2/internal/cri/labels"
	"github.com/containerd/containerd/v2/internal/cri/util"
	snpkg "github.com/containerd/containerd/v2/pkg/snapshotters"
	"github.com/containerd/containerd/v2/pkg/tracing"
)

var (
	imageNameWithRuntimeHandler = "%s,%s"
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
// yanxuean: We can't delete image directly, because we don't know if the image
// is pulled by us. There are resource leakage.
//
// 2) Containerd suggests user to add entry before pulling the image. However if
// an error occurs during the pulling, should we remove the entry from metadata
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
func (c *GRPCCRIImageService) PullImage(ctx context.Context, r *runtime.PullImageRequest) (_ *runtime.PullImageResponse, err error) {

	imageRef := r.GetImage().GetImage()

	credentials := func(host string) (string, string, error) {
		hostauth := r.GetAuth()
		if hostauth == nil {
			config := c.config.Registry.Configs[host]
			if config.Auth != nil {
				hostauth = toRuntimeAuthConfig(*config.Auth)
			}
		}
		return ParseAuth(hostauth, host)
	}

	ref, err := c.CRIImageService.PullImage(ctx, imageRef, credentials, r.SandboxConfig, r.GetImage().GetRuntimeHandler())
	if err != nil {
		return nil, err
	}
	return &runtime.PullImageResponse{ImageRef: ref}, nil
}

func (c *CRIImageService) PullImage(
	ctx context.Context,
	name string,
	credentials func(string) (string, string, error),
	sandboxConfig *runtime.PodSandboxConfig,
	runtimeHandler string) (_ string, err error) {
	span := tracing.SpanFromContext(ctx)
	defer func() {
		// TODO: add domain label for imagePulls metrics, and we may need to provide a mechanism
		// for the user to configure the set of registries that they are interested in.
		if err != nil {
			imagePulls.WithValues("failure").Inc()
		} else {
			imagePulls.WithValues("success").Inc()
		}
	}()

	inProgressImagePulls.Inc()
	defer inProgressImagePulls.Dec()
	startTime := time.Now()

	if credentials == nil {
		credentials = func(host string) (string, string, error) {
			var hostauth *runtime.AuthConfig

			config := c.config.Registry.Configs[host]
			if config.Auth != nil {
				hostauth = toRuntimeAuthConfig(*config.Auth)

			}

			return ParseAuth(hostauth, host)
		}
	}

	namedRef, err := distribution.ParseDockerRef(name)
	if err != nil {
		return "", fmt.Errorf("failed to parse image reference %q: %w", name, err)
	}

	ref := namedRef.String()
	if ref != name {
		log.G(ctx).Debugf("PullImage using normalized image ref: %q", ref)
	}

	imagePullProgressTimeout, err := time.ParseDuration(c.config.ImagePullProgressTimeout)
	if err != nil {
		return "", fmt.Errorf("failed to parse image_pull_progress_timeout %q: %w", c.config.ImagePullProgressTimeout, err)
	}

	snapshotter, platform, err := c.snapshotterFromPodSandboxConfig(ctx, sandboxConfig, runtimeHandler)
	if err != nil {
		return "", err
	}

	log.G(ctx).Debugf("PullImage %q with snapshotter %s, platform %v", ref, snapshotter, platform)
	span.SetAttributes(
		tracing.Attribute("image.ref", ref),
		tracing.Attribute("snapshotter.name", snapshotter),
		tracing.Attribute("platform", platform),
	)
	labels := c.getLabels(ref)

	// If UseLocalImagePull is true, use client.Pull to pull the image, else use transfer service by default.
	//
	// Transfer service does not currently support all the CRI image config options.
	// TODO: Add support for DisableSnapshotAnnotations, DiscardUnpackedLayers, ImagePullWithSyncFs and unpackDuplicationSuppressor
	var image containerd.Image
	if c.config.UseLocalImagePull {
		image, err = c.pullImageWithLocalPull(ctx, ref, credentials, snapshotter, platform, labels, imagePullProgressTimeout)
	} else {
		image, err = c.pullImageWithTransferService(ctx, ref, credentials, snapshotter, platform, labels, imagePullProgressTimeout)
	}
	if err != nil {
		return "", err
	}

	span.AddEvent("Pull and unpack image complete")

	configDesc, err := image.Config(ctx)
	if err != nil {
		return "", fmt.Errorf("get image config descriptor: %w", err)
	}
	imageID := configDesc.Digest.String()

	repoDigest, repoTag := util.GetRepoDigestAndTag(namedRef, image.Target().Digest)
	for _, r := range []string{imageID, repoTag, repoDigest} {
		if r == "" {
			continue
		}
		if err := c.createOrUpdateImageReference(ctx, r, image.Target(), labels); err != nil {
			return "", fmt.Errorf("failed to create image reference %q: %w", r, err)
		}
		// Update image store to reflect the newest state in containerd.
		// No need to use `updateImage`, because the image reference must
		// have been managed by the cri plugin.
		// TODO: Use image service directly
		if err := c.imageStore.Update(ctx, r, runtimeHandler); err != nil {
			return "", fmt.Errorf("failed to update image store %q: %w", r, err)
		}
	}

	const mbToByte = 1024 * 1024
	size, _ := image.Size(ctx)
	imagePullingSpeed := float64(size) / mbToByte / time.Since(startTime).Seconds()
	imagePullThroughput.Observe(imagePullingSpeed)

	log.G(ctx).Infof("Pulled image %q with image id %q, repo tag %q, repo digest %q, size %q in %s", name, imageID,
		repoTag, repoDigest, strconv.FormatInt(size, 10), time.Since(startTime))
	// NOTE(random-liu): the actual state in containerd is the source of truth, even we maintain
	// in-memory image store, it's only for in-memory indexing. The image could be removed
	// by someone else anytime, before/during/after we create the metadata. We should always
	// check the actual state in containerd before using the image or returning status of the
	// image.
	return imageID, nil
}

// pullImageWithLocalPull handles image pulling using the local client.
func (c *CRIImageService) pullImageWithLocalPull(
	ctx context.Context,
	ref string,
	credentials func(string) (string, string, error),
	snapshotter string,
	platform imagespec.Platform,
	labels map[string]string,
	imagePullProgressTimeout time.Duration,
) (containerd.Image, error) {
	pctx, pcancel := context.WithCancel(ctx)
	defer pcancel()
	pullReporter := newPullProgressReporter(ref, pcancel, imagePullProgressTimeout)
	resolver := docker.NewResolver(docker.ResolverOptions{
		Headers: c.config.Registry.Headers,
		Hosts:   c.registryHosts(ctx, credentials, pullReporter.optionUpdateClient),
	})

	log.G(ctx).Debugf("PullImage %q with snapshotter %s using client.Pull()", ref, snapshotter)
	pullOpts := []containerd.RemoteOpt{
		containerd.WithResolver(resolver),
		containerd.WithPullSnapshotter(snapshotter),
		containerd.WithPullUnpack,
		containerd.WithPullLabels(labels),
		containerd.WithDownloadLimiter(c.downloadLimiter),
		containerd.WithMaxConcurrentDownloads(c.config.MaxConcurrentDownloads),
		containerd.WithConcurrentLayerFetchBuffer(c.config.ConcurrentLayerFetchBuffer),
		containerd.WithUnpackOpts([]containerd.UnpackOpt{
			containerd.WithUnpackDuplicationSuppressor(c.unpackDuplicationSuppressor),
			containerd.WithUnpackApplyOpts(diff.WithSyncFs(c.config.ImagePullWithSyncFs)),
		}),
		containerd.WithPlatformMatcher(platforms.Only(platform)),
	}

	// Temporarily removed for v2 upgrade
	//pullOpts = append(pullOpts, c.encryptedImagesPullOpts()...)
	if !c.config.DisableSnapshotAnnotations {
		pullOpts = append(pullOpts,
			containerd.WithImageHandlerWrapper(snpkg.AppendInfoHandlerWrapper(ref)))
	}

	if c.config.DiscardUnpackedLayers {
		// Allows GC to clean layers up from the content store after unpacking
		pullOpts = append(pullOpts,
			containerd.WithChildLabelMap(containerdimages.ChildGCLabelsFilterLayers))
	}

	pullReporter.start(pctx)
	image, err := c.client.Pull(pctx, ref, pullOpts...)
	pcancel()
	if err != nil {
		return nil, fmt.Errorf("failed to pull and unpack image %q: %w", ref, err)
	}
	return image, nil
}

// pullImageWithTransferService handles image pulling using the transfer service.
func (c *CRIImageService) pullImageWithTransferService(
	ctx context.Context,
	ref string,
	credentials func(string) (string, string, error),
	snapshotter string,
	platform imagespec.Platform,
	labels map[string]string,
	imagePullProgressTimeout time.Duration,
) (containerd.Image, error) {
	log.G(ctx).Debugf("PullImage %q with snapshotter %s using transfer service", ref, snapshotter)
	rctx, rcancel := context.WithCancel(ctx)
	defer rcancel()
	transferProgressReporter := newTransferProgressReporter(ref, rcancel, imagePullProgressTimeout)

	// Set image store opts
	var sopts []transferimage.StoreOpt
	sopts = append(sopts, transferimage.WithPlatforms(platform))
	sopts = append(sopts, transferimage.WithUnpack(platforms.DefaultSpec(), snapshotter))
	sopts = append(sopts, transferimage.WithImageLabels(labels))
	is := transferimage.NewStore(ref, sopts...)
	log.G(ctx).Debugf("Getting new CRI credentials")
	ch := newCRICredentials(ref, credentials)
	opts := []registry.Opt{registry.WithCredentials(ch)}
	opts = append(opts, registry.WithHeaders(c.config.Registry.Headers))
	opts = append(opts, registry.WithHostDir(c.config.Registry.ConfigPath))
	reg, err := registry.NewOCIRegistry(ctx, ref, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OCI registry: %w", err)
	}

	transferProgressReporter.start(rctx)
	log.G(ctx).Debugf("Calling cri transfer service")
	err = c.transferrer.Transfer(rctx, reg, is, transfer.WithProgress(transferProgressReporter.createProgressFunc(rctx)))
	rcancel()
	if err != nil {
		return nil, fmt.Errorf("failed to pull and unpack image %q: %w", ref, err)
	}

	// Image should be pulled, unpacked and present in containerd image store at this moment
	image, err := c.client.GetImageWithPlatform(ctx, ref, platform)
	if err != nil {
		return nil, fmt.Errorf("failed to get image %q from containerd image store: %w", ref, err)
	}
	return image, nil
}

// ParseAuth parses AuthConfig and returns username and password/secret required by containerd.
func ParseAuth(auth *runtime.AuthConfig, host string) (string, string, error) {
	if auth == nil {
		return "", "", nil
	}
	if auth.ServerAddress != "" {
		// Do not return the auth info when server address doesn't match.
		u, err := url.Parse(auth.ServerAddress)
		if err != nil {
			return "", "", fmt.Errorf("parse server address: %w", err)
		}
		if host != u.Host {
			return "", "", nil
		}
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
		user, passwd, ok := strings.Cut(string(decoded), ":")
		if !ok {
			return "", "", fmt.Errorf("invalid decoded auth: %q", decoded)
		}
		return user, strings.Trim(passwd, "\x00"), nil
	}
	// TODO(random-liu): Support RegistryToken.
	// An empty auth config is valid for anonymous registry
	return "", "", nil
}

// createOrUpdateImageReference creates or updates image reference inside containerd image store.
// Note that because create and update are not finished in one transaction, there could be race. E.g.
// the image reference is deleted by someone else after create returns already exists, but before update
// happens.
func (c *CRIImageService) createOrUpdateImageReference(ctx context.Context, name string, desc imagespec.Descriptor, labels map[string]string) error {
	img := containerdimages.Image{
		Name:   name,
		Target: desc,
		// Add a label to indicate that the image is managed by the cri plugin.
		Labels: labels,
	}

	// TODO(random-liu): Figure out which is the more performant sequence create then update or
	// update then create.
	// TODO: Call CRIImageService directly
	_, err := c.images.Create(ctx, img)
	if err == nil {
		return nil
	} else if !errdefs.IsAlreadyExists(err) {
		return err
	}

	// Retrieve oldImg from image store here because Create routine returns an
	// empty image on ErrAlreadyExists
	oldImg, err := c.images.Get(ctx, img.Name)
	if err != nil {
		return err
	}
	fieldpaths := []string{"target"}
	if oldImg.Labels[crilabels.ImageLabelKey] != labels[crilabels.ImageLabelKey] {
		fieldpaths = append(fieldpaths, "labels."+crilabels.ImageLabelKey)
	}
	if oldImg.Labels[crilabels.PinnedImageLabelKey] != labels[crilabels.PinnedImageLabelKey] &&
		labels[crilabels.PinnedImageLabelKey] == crilabels.PinnedImageLabelValue {
		fieldpaths = append(fieldpaths, "labels."+crilabels.PinnedImageLabelKey)
	}
	if oldImg.Target.Digest == img.Target.Digest && len(fieldpaths) < 2 {
		return nil
	}
	_, err = c.images.Update(ctx, img, fieldpaths...)
	return err
}

// getLabels get image labels to be added on CRI image
func (c *CRIImageService) getLabels(name string) map[string]string {
	labels := map[string]string{crilabels.ImageLabelKey: crilabels.ImageLabelValue}
	for _, pinned := range c.config.PinnedImages {
		if pinned == name {
			labels[crilabels.PinnedImageLabelKey] = crilabels.PinnedImageLabelValue
		}
	}
	return labels
}

func buildNewLabels(
	rootImgLabels map[string]string,
	criLabels map[string]string,
	imgInfo images.UnpackedImageInfo,
) map[string]string {
	newLabels := make(map[string]string, len(rootImgLabels)+len(criLabels))
	for key, val := range rootImgLabels {
		newLabels[key] = val
	}
	for key, value := range criLabels {
		// Ensure to copy the criLabels so they are updated as well. This is important
		// for images pulled via ctr or transfer service
		newLabels[key] = value
	}
	snapshotLabel := fmt.Sprintf("%s.%s", images.GcSnapshotLabel, imgInfo.Snapshot)
	newLabels[snapshotLabel] = imgInfo.SnapshotID
	return newLabels
}

// findRuntimeHandlers finds all the matching
// RuntimePlatforms with identical platform and snapshot strings
func (c *CRIImageService) findRuntimeHandlers(ctx context.Context, platform imagespec.Platform, snapshot string) []string {
	var runtimeHandlers []string
	log.G(ctx).Tracef("Find valid runtimeHandlers for platform %v", platform)
	for handler, runtimePlatform := range c.runtimePlatforms {
		if runtimePlatform.Platform.Architecture != platform.Architecture ||
			runtimePlatform.Platform.OS != platform.OS ||
			!strings.Contains(platform.OSVersion, runtimePlatform.Platform.OSVersion) ||
			runtimePlatform.Snapshotter != snapshot {
			continue
		}
		runtimeHandlers = append(runtimeHandlers, handler)
	}

	return runtimeHandlers
}

// updateRootImgLabels updated rootImg with images.GcExpirationLabel
// and images.RootImageLabel
func (c *CRIImageService) updateRootImgLabels(
	ctx context.Context,
	rootImg images.Image,
	imgInfo images.UnpackedImageInfo,
) error {
	fieldpaths := []string{"labels"}
	updateRootImg := containerdimages.Image{
		Target: rootImg.Target,
		Labels: rootImg.Labels,
	}
	for _, r := range []string{rootImg.Name, imgInfo.ConfigDigest.String()} {
		updateRootImg.Name = r
		if updateRootImg.Labels == nil {
			updateRootImg.Labels = map[string]string{}
		}
		updateRootImg.Labels[images.GcExpirationLabel] = time.Now().Format(time.RFC3339)
		// This label is used to mark it as root image
		updateRootImg.Labels[images.RootImageLabel] = "nil"

		_, err := c.images.Update(ctx, updateRootImg, fieldpaths...)
		if err != nil {
			return fmt.Errorf("failed to update labels on root image %v", rootImg.Name)
		}
	}
	return nil
}

// updateContentSnapshotLabels adds labels ro corresponding entry in content store.
// Add a snapshot info label to the configDesc to help subsequent calls identify snapshotID.
// Remove snapshot gc label on configDesc as it no longer needs to hold reference to it.
func (c *CRIImageService) updateContentSnapshotLabels(
	ctx context.Context,
	imgInfo images.UnpackedImageInfo,
) error {
	contentInfo, err := c.content.Info(ctx, imgInfo.ConfigDigest)
	if err != nil {
		return fmt.Errorf("failed to get content info for %q: %w", imgInfo.ConfigDigest.String(), err)
	}
	if contentInfo.Labels == nil {
		contentInfo.Labels = make(map[string]string)
	}

	// Add snapshot info label to the configDesc to help subsequent calls identify snapshotID.
	// GcSnapshotLabel is removed as the image tuple (ref, runtimehandler) now has reference
	// to the unpacked snapshot. This helps with correctly garbage collecting the snapshot.
	snapshotInfoLabel := fmt.Sprintf("%s.%s", images.SnapshotInfoLabel, imgInfo.Snapshot)
	snapshotGcLabel := fmt.Sprintf("%s.%s", images.GcSnapshotLabel, imgInfo.Snapshot)
	contentInfo.Labels[snapshotInfoLabel] = imgInfo.SnapshotID
	delete(contentInfo.Labels, snapshotGcLabel)

	if _, err := c.content.Update(ctx, contentInfo, "labels"); err != nil {
		return fmt.Errorf("update content store labels for %q: %w", imgInfo.ConfigDigest.String(), err)
	}
	return nil
}

// updateWithValidRuntimeHandlers finds all the matching
// runtimehandlers for a given image ref, adds new image labels
// and updates the containerd metadata store with an entry each
// for (ref, runtimeHandler) tuple.
// For every matching runtime handler, the following two new
// image labels are created:
// a. New snapshot label to point to the unpacked image
// b. New image label to point to the root image of this ref
func (c *CRIImageService) updateWithValidRuntimeHandlers(ctx context.Context, ref string) error {
	rootImg, err := c.images.Get(ctx, ref)
	if err != nil {
		// Image not found in containerd store
		return fmt.Errorf("error getting root image from metdata store: %v", err)
	}

	// 1. Walk the image from the root and record the
	// corresponding platform, snapshot and snapshotID
	// that the image has been unpacked for.
	imgInfo, err := images.GetUnpackedImageInfo(ctx, c.content, rootImg.Target)
	if err != nil {
		log.G(ctx).WithError(err).Debugf("Corresponding platform/snapshot not found for image: %v", ref)
		return nil
	}

	// 2. Use imgInfo.Platforms and imgInfo.Snapshots to find
	// all matching runtime handlers
	runtimeHandlers := c.findRuntimeHandlers(ctx, imgInfo.Platform, imgInfo.Snapshot)
	if len(runtimeHandlers) == 0 {
		runtimeHandlers = append(runtimeHandlers, c.defaultRuntimeName)
	}

	// 3. Create new entries in containerd image store with name
	// in the form of tuple (ref, runtimeHandler).
	// Additionally add two new labels are added to hold reference
	// to the right objects to help with garbage collection:
	// a. Add images.GcImageLabel pointing to reference 'r' to
	// have reference to root image.
	// b. Add snapshot gc label to the unpacked snapshot
	// to have a reference to it.
	// This prevents garbage collection from cleaning up the root
	// image before all the image entries referencing it are removed.
	newLabels := buildNewLabels(rootImg.Labels, c.getLabels(ref), imgInfo)
	imageID := imgInfo.ConfigDigest.String()
	for _, runtimeHandler := range runtimeHandlers {
		// Add the (id,runtimeHandler) + (ref,runtimeHandler)
		for _, r := range []string{ref, imageID} {
			if r == "" {
				continue
			}
			refWithRuntimeHandler := fmt.Sprintf(imageNameWithRuntimeHandler, r, runtimeHandler)
			if _, err := c.client.GetImage(ctx, refWithRuntimeHandler); err == nil {
				continue
			} else if !errdefs.IsNotFound(err) {
				return fmt.Errorf("error getting containerd image by reference: %w", err)
			}

			// Set gc label to root image to keep reference to it.
			newLabels[images.GcImageLabel] = r
			if err := c.createOrUpdateImageReference(ctx, refWithRuntimeHandler, rootImg.Target, newLabels); err != nil {
				return fmt.Errorf("failed to create image reference %q: %w", refWithRuntimeHandler, err)
			}
			// Update image store to reflect the newest state in containerd.
			if err = c.imageStore.Update(ctx, r, runtimeHandler); err != nil {
				return fmt.Errorf("failed to update CRI cache store for image %v: %w", refWithRuntimeHandler, err)
			}
		}
	}

	// Once new entries have been added, add gc expiration label
	// to root image in order to facilitate gc cleanup when
	// all references to it have been removed.
	if err := c.updateRootImgLabels(ctx, rootImg, imgInfo); err != nil {
		return err
	}

	// Add a snapshot info label to the configDesc to help subsequent calls identify snapshotID.
	// Snapshot gc labels have been appropriately added to the newly added image labels.
	// They are the ones that truly need to hold reference to the unpacked snapshot.
	// Therefore, get rid of snapshot gc on the configDesc as it no longer needs to
	// hold reference to it.
	if err = c.updateContentSnapshotLabels(ctx, imgInfo); err != nil {
		return err
	}
	return nil
}

// GetRuntimeHandlerFromRef returns the ref and runtimehandler if specified.
// ref could be a tuple of (imageRef, runtimeHandler).
// If no tuple was specified, empty string is returned for runtimehandler.
func GetRuntimeHandlerFromRef(ref string) (imageName, runtimeHandler string) {
	parts := strings.SplitN(ref, ",", 2)
	imageName = parts[0]
	if len(parts) == 2 && parts[1] != "" {
		runtimeHandler = parts[1]
	}
	return
}

// UpdateImage updates image store to reflect the newest state of an image reference
// in containerd. If the reference is not managed by the cri plugin, the function also
// generates necessary metadata for the image and make it managed.
func (c *CRIImageService) UpdateImage(ctx context.Context, r string) error {
	// Check if this ref has runtimehandler associated with it
	ref, runtimeHandler := GetRuntimeHandlerFromRef(r)
	if runtimeHandler != "" {
		// TODO: Use image service
		_, err := c.images.Get(ctx, r)
		if err != nil {
			if !errdefs.IsNotFound(err) {
				return fmt.Errorf("get image by reference: %w", err)
			}
			// If the image is not found, we should continue updating the cache,
			// so that all the image, runtimeHandler entries can be removed from the cri cache.
			if err := c.imageStore.Update(ctx, ref, runtimeHandler); err != nil {
				return fmt.Errorf("update image store for %q: %w", r, err)
			}
			return nil
		}
		// If entry for this tuple (ref, runtimehandler) was found in containerd
		// metadata store, then update CRI's image cache accordingly too.
		err = c.imageStore.Update(ctx, ref, runtimeHandler)
		if err != nil {
			return fmt.Errorf("failed to update CRI image cache for image %q: %w", r, err)
		}
		return nil
	}

	// TODO: Use image service
	rootImg, err := c.client.GetImage(ctx, r)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("get image by reference: %w", err)
		}
		log.G(ctx).Debugf("Root image not found. Clean up CRI image cache")
		// If the root image is not found, we are in a bad state. Remove all references
		// for this image from CRI cache ad containerd store
		if err := c.imageStore.RemoveReference(ctx, r); err != nil {
			return fmt.Errorf("update image store for %q failed: %w", r, err)
		}
		return nil
	}

	// If it has rootImage label set, do not continue as containerd image store
	// would have already been updated with all the matching runtime handlers previously.
	rootImgLabels := rootImg.Labels()
	if rootImgLabels != nil {
		if _, ok := rootImgLabels[images.RootImageLabel]; ok {
			return nil
		}
	}

	labels := rootImg.Labels()
	criLabels := c.getLabels(r)
	for key, value := range criLabels {
		if labels[key] != value {
			// Make sure the image has the image id as its unique
			// identifier that references the image in its lifetime.
			configDesc, err := rootImg.Config(ctx)
			if err != nil {
				return fmt.Errorf("get image id: %w", err)
			}
			id := configDesc.Digest.String()
			if err := c.createOrUpdateImageReference(ctx, id, rootImg.Target(), criLabels); err != nil {
				return fmt.Errorf("create image id reference %q: %w", id, err)
			}
			// The image id is ready, add the label to mark the image as managed.
			if err := c.createOrUpdateImageReference(ctx, r, rootImg.Target(), criLabels); err != nil {
				return fmt.Errorf("create managed label: %w", err)
			}
			break
		}
	}

	// Update containerd image store and CRI image cache with all valid runtimehandlers for this image.
	err = c.updateWithValidRuntimeHandlers(ctx, r)
	if err != nil {
		return fmt.Errorf("failed to update containerd and CRI image store for valid runtimehandlers: %v", err)
	}
	return nil
}

func hostDirFromRoots(roots []string) func(string) (string, error) {
	rootfn := make([]func(string) (string, error), len(roots))
	for i := range roots {
		rootfn[i] = config.HostDirFromRoot(roots[i])
	}
	return func(host string) (dir string, err error) {
		for _, fn := range rootfn {
			dir, err = fn(host)
			if (err != nil && !errdefs.IsNotFound(err)) || (dir != "") {
				break
			}
		}
		return
	}
}

// registryHosts is the registry hosts to be used by the resolver.
func (c *CRIImageService) registryHosts(ctx context.Context, credentials func(host string) (string, string, error), updateClientFn config.UpdateClientFunc) docker.RegistryHosts {
	paths := filepath.SplitList(c.config.Registry.ConfigPath)
	if len(paths) > 0 {
		hostOptions := config.HostOptions{
			UpdateClient: updateClientFn,
		}
		hostOptions.Credentials = credentials
		hostOptions.HostDir = hostDirFromRoots(paths)
		// need to pass cri global headers to per-host authorizers
		hostOptions.AuthorizerOpts = []docker.AuthorizerOpt{
			docker.WithAuthHeader(c.config.Registry.Headers),
		}

		return config.ConfigureHosts(ctx, hostOptions)
	}

	return func(host string) ([]docker.RegistryHost, error) {
		var registries []docker.RegistryHost

		endpoints, err := c.registryEndpoints(host)
		if err != nil {
			return nil, fmt.Errorf("get registry endpoints: %w", err)
		}
		for _, e := range endpoints {
			u, err := url.Parse(e)
			if err != nil {
				return nil, fmt.Errorf("parse registry endpoint %q from mirrors: %w", e, err)
			}

			var (
				transport = docker.DefaultHTTPTransport(nil) // no tls config
				client    = &http.Client{Transport: transport}
				config    = c.config.Registry.Configs[u.Host]
			)

			if docker.IsLocalhost(host) && u.Scheme == "http" {
				// Skipping TLS verification for localhost
				transport.TLSClientConfig = &tls.Config{
					InsecureSkipVerify: true,
				}
			}

			// Make a copy of `credentials`, so that different authorizers would not reference
			// the same credentials variable.
			credentials := credentials
			if credentials == nil && config.Auth != nil {
				auth := toRuntimeAuthConfig(*config.Auth)
				credentials = func(host string) (string, string, error) {
					return ParseAuth(auth, host)
				}
			}

			if updateClientFn != nil {
				if err := updateClientFn(client); err != nil {
					return nil, fmt.Errorf("failed to update http client: %w", err)
				}
			}

			authorizer := docker.NewDockerAuthorizer(
				docker.WithAuthClient(client),
				docker.WithAuthCreds(credentials),
				docker.WithAuthHeader(c.config.Registry.Headers),
			)

			if u.Path == "" {
				u.Path = "/v2"
			}

			registries = append(registries, docker.RegistryHost{
				Client:       client,
				Authorizer:   authorizer,
				Host:         u.Host,
				Scheme:       u.Scheme,
				Path:         u.Path,
				Capabilities: docker.HostCapabilityResolve | docker.HostCapabilityPull,
			})
		}
		return registries, nil
	}
}

// toRuntimeAuthConfig converts cri plugin auth config to runtime auth config.
func toRuntimeAuthConfig(a criconfig.AuthConfig) *runtime.AuthConfig {
	return &runtime.AuthConfig{
		Username:      a.Username,
		Password:      a.Password,
		Auth:          a.Auth,
		IdentityToken: a.IdentityToken,
	}
}

// defaultScheme returns the default scheme for a registry host.
func defaultScheme(host string) string {
	if docker.IsLocalhost(host) {
		return "http"
	}
	return "https"
}

// addDefaultScheme returns the endpoint with default scheme
func addDefaultScheme(endpoint string) (string, error) {
	if strings.Contains(endpoint, "://") {
		return endpoint, nil
	}
	ue := "dummy://" + endpoint
	u, err := url.Parse(ue)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s://%s", defaultScheme(u.Host), endpoint), nil
}

// registryEndpoints returns endpoints for a given host.
// It adds default registry endpoint if it does not exist in the passed-in endpoint list.
// It also supports wildcard host matching with `*`.
func (c *CRIImageService) registryEndpoints(host string) ([]string, error) {
	var endpoints []string
	_, ok := c.config.Registry.Mirrors[host]
	if ok {
		endpoints = c.config.Registry.Mirrors[host].Endpoints
	} else {
		endpoints = c.config.Registry.Mirrors["*"].Endpoints
	}
	defaultHost, err := docker.DefaultHost(host)
	if err != nil {
		return nil, fmt.Errorf("get default host: %w", err)
	}
	for i := range endpoints {
		en, err := addDefaultScheme(endpoints[i])
		if err != nil {
			return nil, fmt.Errorf("parse endpoint url: %w", err)
		}
		endpoints[i] = en
	}
	for _, e := range endpoints {
		u, err := url.Parse(e)
		if err != nil {
			return nil, fmt.Errorf("parse endpoint url: %w", err)
		}
		if u.Host == host {
			// Do not add default if the endpoint already exists.
			return endpoints, nil
		}
	}
	return append(endpoints, defaultScheme(defaultHost)+"://"+defaultHost), nil
}

// encryptedImagesPullOpts returns the necessary list of pull options required
// for decryption of encrypted images based on the cri decryption configuration.
// Temporarily removed for v2 upgrade
func (c *CRIImageService) encryptedImagesPullOpts() []containerd.RemoteOpt {
	if c.config.ImageDecryption.KeyModel == criconfig.KeyModelNode {
		ltdd := imgcrypt.Payload{}
		decUnpackOpt := encryption.WithUnpackConfigApplyOpts(encryption.WithDecryptedUnpack(&ltdd))
		opt := containerd.WithUnpackOpts([]containerd.UnpackOpt{decUnpackOpt})
		return []containerd.RemoteOpt{opt}
	}
	return nil
}

const (
	// defaultPullProgressReportInterval represents that how often the
	// reporter checks that pull progress.
	defaultPullProgressReportInterval = 10 * time.Second
)

// pullProgressReporter is used to check single PullImage progress.
type pullProgressReporter struct {
	ref         string
	cancel      context.CancelFunc
	reqReporter pullRequestReporter
	timeout     time.Duration
}

func newPullProgressReporter(ref string, cancel context.CancelFunc, timeout time.Duration) *pullProgressReporter {
	return &pullProgressReporter{
		ref:         ref,
		cancel:      cancel,
		reqReporter: pullRequestReporter{},
		timeout:     timeout,
	}
}

func (reporter *pullProgressReporter) optionUpdateClient(client *http.Client) error {
	client.Transport = &pullRequestReporterRoundTripper{
		rt:          client.Transport,
		reqReporter: &reporter.reqReporter,
	}
	return nil
}

func (reporter *pullProgressReporter) start(ctx context.Context) {
	if reporter.timeout == 0 {
		log.G(ctx).Infof("no timeout and will not start pulling image %s reporter", reporter.ref)
		return
	}

	go func() {
		var (
			reportInterval = defaultPullProgressReportInterval

			lastSeenBytesRead = uint64(0)
			lastSeenTimestamp = time.Now()
		)

		// check progress more frequently if timeout < default internal
		if reporter.timeout < reportInterval {
			reportInterval = reporter.timeout / 2
		}

		var ticker = time.NewTicker(reportInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				activeReqs, bytesRead := reporter.reqReporter.status()

				log.G(ctx).WithField("ref", reporter.ref).
					WithField("activeReqs", activeReqs).
					WithField("totalBytesRead", bytesRead).
					WithField("lastSeenBytesRead", lastSeenBytesRead).
					WithField("lastSeenTimestamp", lastSeenTimestamp.Format(time.RFC3339)).
					WithField("reportInterval", reportInterval).
					Debugf("progress for image pull")

				if activeReqs == 0 || bytesRead > lastSeenBytesRead {
					lastSeenBytesRead = bytesRead
					lastSeenTimestamp = time.Now()
					continue
				}

				if time.Since(lastSeenTimestamp) > reporter.timeout {
					log.G(ctx).Errorf("cancel pulling image %s because of no progress in %v", reporter.ref, reporter.timeout)
					reporter.cancel()
					return
				}
			case <-ctx.Done():
				activeReqs, bytesRead := reporter.reqReporter.status()
				log.G(ctx).Infof("stop pulling image %s: active requests=%v, bytes read=%v", reporter.ref, activeReqs, bytesRead)
				return
			}
		}
	}()
}

// countingReadCloser wraps http.Response.Body with pull request reporter,
// which is used by pullRequestReporterRoundTripper.
type countingReadCloser struct {
	once sync.Once

	rc          io.ReadCloser
	reqReporter *pullRequestReporter
}

// Read reads bytes from original io.ReadCloser and increases bytes in
// pull request reporter.
func (r *countingReadCloser) Read(p []byte) (int, error) {
	n, err := r.rc.Read(p)
	r.reqReporter.incByteRead(uint64(n))
	return n, err
}

// Close closes the original io.ReadCloser and only decreases the number of
// active pull requests once.
func (r *countingReadCloser) Close() error {
	err := r.rc.Close()
	r.once.Do(r.reqReporter.decRequest)
	return err
}

// pullRequestReporter is used to track the progress per each criapi.PullImage.
type pullRequestReporter struct {
	// activeReqs indicates that current number of active pulling requests,
	// including auth requests.
	activeReqs atomic.Int32
	// totalBytesRead indicates that the total bytes has been read from
	// remote registry.
	totalBytesRead atomic.Uint64
}

func (reporter *pullRequestReporter) incRequest() {
	reporter.activeReqs.Add(1)
}

func (reporter *pullRequestReporter) decRequest() {
	reporter.activeReqs.Add(-1)
}

func (reporter *pullRequestReporter) incByteRead(nr uint64) {
	reporter.totalBytesRead.Add(nr)
}

func (reporter *pullRequestReporter) status() (currentReqs int32, totalBytesRead uint64) {
	currentReqs = reporter.activeReqs.Load()
	totalBytesRead = reporter.totalBytesRead.Load()
	return currentReqs, totalBytesRead
}

// pullRequestReporterRoundTripper wraps http.RoundTripper with pull request
// reporter which is used to track the progress of active http request with
// counting readable http.Response.Body.
//
// NOTE:
//
// Although containerd provides ingester maninternal/cri/server/container_create.goager to track the progress
// of pulling request, for example `ctr image pull` shows the console progress
// bar, it needs more CPU resources to open/read the ingested files with
// acquiring containerd metadata plugin's boltdb lock.
//
// Before sending HTTP request to registry, the containerd.Client.Pull library
// will open writer by containerd ingester manager. Based on this, the
// http.RoundTripper wrapper can track the active progress with lower overhead
// even if the ref has been locked in ingester manager by other Pull request.
type pullRequestReporterRoundTripper struct {
	rt http.RoundTripper

	reqReporter *pullRequestReporter
}

func (rt *pullRequestReporterRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.reqReporter.incRequest()

	resp, err := rt.rt.RoundTrip(req)
	if err != nil {
		rt.reqReporter.decRequest()
		return nil, err
	}

	resp.Body = &countingReadCloser{
		rc:          resp.Body,
		reqReporter: rt.reqReporter,
	}
	return resp, err
}

type criCredentials struct {
	ref         string
	credentials func(string) (string, string, error)
}

func newCRICredentials(ref string, credentials func(string) (string, string, error)) registry.CredentialHelper {
	return &criCredentials{
		ref:         ref,
		credentials: credentials,
	}
}

// GetCredentials gets credential from criCredentials makes criCredentials a registry.CredentialHelper
func (cc *criCredentials) GetCredentials(ctx context.Context, ref string, host string) (registry.Credentials, error) {
	if cc.credentials == nil {
		return registry.Credentials{}, fmt.Errorf("credential handler not initialized for ref %q", ref)
	}

	if ref != cc.ref {
		return registry.Credentials{}, fmt.Errorf("invalid ref %q, expected %q", ref, cc.ref)
	}

	username, secret, err := cc.credentials(host)
	if err != nil {
		return registry.Credentials{}, fmt.Errorf("failed to get credentials for %q: %w", host, err)
	}
	return registry.Credentials{
		Host:     host,
		Username: username,
		Secret:   secret,
	}, nil
}

type transferProgressReporter struct {
	ref               string
	pc                chan transfer.Progress
	cancel            context.CancelFunc
	timeout           time.Duration
	reqReporter       pullRequestReporter
	statuses          map[string]*transfer.Progress
	lastSeenBytesRead uint64
	lastSeenTimestamp time.Time
}

func newTransferProgressReporter(ref string, cancel context.CancelFunc, timeout time.Duration) *transferProgressReporter {
	return &transferProgressReporter{
		ref:      ref,
		cancel:   cancel,
		timeout:  timeout,
		pc:       make(chan transfer.Progress),
		statuses: make(map[string]*transfer.Progress),
	}
}

func (reporter *transferProgressReporter) handleProgress(p transfer.Progress) {
	// We only need to handle Progress Nodes that represent
	// valid requests to a remote registry, so Progress nodes
	// without 'Name', 'Desc' or 'Total' can be ignored
	if p.Name == "" || p.Desc == nil || p.Total == 0 {
		return
	}

	switch p.Event {
	case "waiting":
		// 'Waiting' events can be either when the layer is waiting to be
		// downloaded and no progress has been made. Or when we have made
		// some progress but `waiting` for more content to be downloaded.
		if p.Progress == 0 {
			return
		}
		fallthrough // Handle non-zero waiting progress same as downloading
	case "downloading":
		var curProgress int64
		if node, ok := reporter.statuses[p.Name]; !ok {
			curProgress = p.Progress
			reporter.reqReporter.incRequest()
		} else {
			curProgress = p.Progress - node.Progress
		}
		reporter.statuses[p.Name] = &p
		if curProgress > 0 {
			reporter.IncBytesRead(curProgress)
		}

		// Download may be complete, but waiting for content
		// to be written. In this case, we no longer consider it
		// as an active requests.
		if p.Progress == p.Total {
			reporter.reqReporter.decRequest()
			delete(reporter.statuses, p.Name)
		}

	case "complete":
		if node, exists := reporter.statuses[p.Name]; exists {
			if curProgress := p.Progress - node.Progress; curProgress > 0 {
				reporter.IncBytesRead(curProgress)
			}
			reporter.reqReporter.decRequest()
			delete(reporter.statuses, p.Name)
		}
	default:
		return
	}
}

func (reporter *transferProgressReporter) IncBytesRead(bytes int64) {
	reporter.reqReporter.incByteRead(uint64(bytes))
}

func (reporter *transferProgressReporter) start(ctx context.Context) {
	if reporter.timeout == 0 {
		log.G(ctx).Infof("no timeout and will not start pulling image %s reporter", reporter.ref)
		return
	}

	go func() {
		var (
			reportInterval = defaultPullProgressReportInterval
		)

		reporter.lastSeenBytesRead = uint64(0)
		reporter.lastSeenTimestamp = time.Now()

		// check progress more frequently if timeout < default internal
		if reporter.timeout < reportInterval {
			reportInterval = reporter.timeout / 2
		}

		var ticker = time.NewTicker(reportInterval)
		defer ticker.Stop()

		for {
			select {
			case p := <-reporter.pc:
				reporter.handleProgress(p)
			case <-ticker.C:
				reporter.checkProgress(ctx, reportInterval)
				continue
			case <-ctx.Done():
				activeReqs, bytesRead := reporter.reqReporter.status()
				log.G(ctx).Infof("stop pulling image %s: active requests=%v, bytes read=%v", reporter.ref, activeReqs, bytesRead)
				return
			}
		}
	}()
}

func (reporter *transferProgressReporter) checkProgress(ctx context.Context, reportInterval time.Duration) {
	activeReqs, bytesRead := reporter.reqReporter.status()

	lastSeenBytesRead := reporter.lastSeenBytesRead
	lastSeenTimestamp := reporter.lastSeenTimestamp

	log.G(ctx).WithField("ref", reporter.ref).
		WithField("activeReqs", activeReqs).
		WithField("totalBytesRead", bytesRead).
		WithField("lastSeenBytesRead", lastSeenBytesRead).
		WithField("lastSeenTimestamp", lastSeenTimestamp.Format(time.RFC3339)).
		WithField("reportInterval", reportInterval).
		Debugf("progress for image pull")

	if activeReqs == 0 || bytesRead > lastSeenBytesRead {
		reporter.lastSeenBytesRead = bytesRead
		reporter.lastSeenTimestamp = time.Now()
		return
	}

	if time.Since(lastSeenTimestamp) > reporter.timeout {
		log.G(ctx).Errorf("cancel pulling image %s because of no progress in %v", reporter.ref, reporter.timeout)
		reporter.cancel()
	}
}

func (reporter *transferProgressReporter) createProgressFunc(ctx context.Context) transfer.ProgressFunc {
	return func(p transfer.Progress) {
		select {
		case reporter.pc <- p:
		case <-ctx.Done():
			return
		}
	}
}

// getInfoFromRuntimePlatforms validates the CRI runtime handler and passed the default
// snapshotter and platform values if no such runtime handler was defined.
func (c *CRIImageService) getInfoFromRuntimePlatforms(ctx context.Context, criRuntimeHandler string) (string, imagespec.Platform, error) {
	defaultSnapshotter := c.config.Snapshotter
	defaultPlatform := platforms.DefaultSpec()

	if c.runtimePlatforms == nil {
		log.G(ctx).Debugf("no CRIImageService.runtimePlatforms defined")
		return defaultSnapshotter, defaultPlatform, nil
	}
	if _, ok := c.runtimePlatforms[criRuntimeHandler]; !ok {
		log.G(ctx).Debugf("CRI runtimehandler %v not found in CRI image config", criRuntimeHandler)
		return defaultSnapshotter, defaultPlatform, fmt.Errorf("CRI runtimehandler %v not found in CRI image config", criRuntimeHandler)
	}
	imagePlatform := c.runtimePlatforms[criRuntimeHandler]
	return imagePlatform.Snapshotter, imagePlatform.Platform, nil
}

// RuntimeHandler information is passed through PullImageRequest from cri-api v0.29.0.
// Therefore, attempt to read the snapshot and runtimeHandler information from PullImageRequest
// and if one was not specified, try to look for appropriate annotation key. If none are found,
// return default values for snapshotter and runtime handler.
func (c *CRIImageService) snapshotterFromPodSandboxConfig(ctx context.Context,
	s *runtime.PodSandboxConfig, runtimeHandlerFromCRI string) (string, imagespec.Platform, error) {
	snapshotter := c.config.Snapshotter
	platform := platforms.DefaultSpec()

	// Honor the CRI runtime handler field passed through PullImageRequest.
	// If the runtimeHandler is valid, we have to pass both snapshotter and
	// platform defined in c.RuntimePlatforms. If either of the fields
	// in c.RuntimPlatforms[runtimehandler] are empty, fallback to using the
	// default snapshotter and platform.
	if runtimeHandlerFromCRI != "" {
		return c.getInfoFromRuntimePlatforms(ctx, runtimeHandlerFromCRI)
	}

	if s == nil || s.Annotations == nil {
		return snapshotter, platform, nil
	}

	// runtimeHandler from CRI was "", attempt to read runtimeHandler from annotations
	runtimeHandler, ok := s.Annotations[annotations.RuntimeHandler]
	if ok {
		return c.getInfoFromRuntimePlatforms(ctx, runtimeHandler)
	}

	return snapshotter, platform, nil
}
