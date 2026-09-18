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
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	containerdimages "github.com/containerd/containerd/v2/core/images"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/platforms"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// ImageStatus returns the status of the image, returns nil if the image isn't present.
// TODO(random-liu): We should change CRI to distinguish image id and image spec. (See
// kubernetes/kubernetes#46255)
func (c *CRIImageService) ImageStatus(ctx context.Context, r *runtime.ImageStatusRequest) (*runtime.ImageStatusResponse, error) {
	image, err := c.LocalResolve(r.GetImage().GetImage())
	if err != nil {
		if errdefs.IsNotFound(err) {
			// return empty without error when image not found.
			return &runtime.ImageStatusResponse{}, nil
		}
		return nil, fmt.Errorf("can not resolve %q locally: %w", r.GetImage().GetImage(), err)
	}

	// Verify the corresponding snapshot exists for the snapshotter that would
	// be used by the next container creation. If it doesn't, the image is not
	// usable as-is (e.g. layer content has been discarded by
	// discard_unpacked_layers and the snapshotter has since changed) and
	// reporting it absent prompts the caller to re-pull it with credentials
	// they hold, restoring any missing layer content along the way. This fires
	// when kubelet passes the pod's runtime handler (RuntimeClassInImageCriAPI
	// feature gate enabled), so the per-runtime snapshotter is consulted.
	if !c.imageSnapshotExists(ctx, image, r.GetImage()) {
		return &runtime.ImageStatusResponse{}, nil
	}

	// When discard_unpacked_layers is false, every layer blob should be in the
	// content store. If any is missing, the image is in the corrupt state left
	// behind by a prior discard_unpacked_layers=true run (which discarded the
	// blobs while keeping the manifest and snapshot intact). A present snapshot
	// is not sufficient: re-unpacking the image — for another snapshotter, or
	// any operation that needs the layers — requires the blobs, and the CRI
	// container path has no fetcher of its own. This check is independent of
	// imageSnapshotExists on purpose: it is the only signal available when the
	// runtime handler is not passed (RuntimeClassInImageCriAPI disabled), where
	// the snapshot check only ever sees the default snapshotter. Reporting the
	// image absent triggers kubelet (which holds the pull credentials) to
	// re-pull, and the unpacker repopulates the blobs even when it
	// short-circuits on an existing snapshot.
	if !c.config.DiscardUnpackedLayers && !c.imageBlobsPresent(ctx, image) {
		return &runtime.ImageStatusResponse{}, nil
	}

	runtimeImage := toCRIImage(image)
	info, err := c.toCRIImageInfo(ctx, &image, r.GetVerbose())
	if err != nil {
		return nil, fmt.Errorf("failed to generate image info: %w", err)
	}

	return &runtime.ImageStatusResponse{
		Image: runtimeImage,
		Info:  info,
	}, nil
}

// targetSnapshotter resolves the snapshotter that the runtime handler in spec
// would use for the next container creation, mirroring RuntimeSnapshotter in
// the container-create path: a per-runtime snapshotter override if the handler
// has one, otherwise the configured default. This is the only image-state axis
// that varies per runtime here — the platform does not, because the CRI image
// store records a single chainID computed for platforms.Default() and the
// container-create unpack (unpackImage) likewise unpacks platforms.Default().
func (c *CRIImageService) targetSnapshotter(spec *runtime.ImageSpec) string {
	snapshotter := c.config.Snapshotter
	if spec != nil {
		if rh := spec.GetRuntimeHandler(); rh != "" {
			if p, ok := c.runtimePlatforms[rh]; ok && p.Snapshotter != "" {
				snapshotter = p.Snapshotter
			}
		}
	}
	return snapshotter
}

// imageSnapshotExists reports whether the chainID-rooted snapshot for the
// given image exists in the snapshotter that would handle the next container
// creation. It returns false only when the snapshot is conclusively missing;
// whenever the check cannot be performed conclusively — missing chainID, no
// tracked snapshotters, the target snapshotter not registered with this
// service, or a transient snapshotter error — it returns true so that the
// usability check never turns into an availability regression for ImageStatus.
func (c *CRIImageService) imageSnapshotExists(ctx context.Context, image imagestore.Image, spec *runtime.ImageSpec) bool {
	if image.ChainID == "" {
		return true
	}
	if len(c.snapshotters) == 0 {
		return true
	}
	snapshotterName := c.targetSnapshotter(spec)
	sn, ok := c.snapshotters[snapshotterName]
	if !ok {
		return true
	}
	if _, err := sn.Stat(ctx, image.ChainID); err != nil {
		if errdefs.IsNotFound(err) {
			log.G(ctx).Debugf("ImageStatus: snapshot %q not found in snapshotter %q for image %q; reporting as not present so it will be re-pulled", image.ChainID, snapshotterName, image.ID)
			return false
		}
		log.G(ctx).WithError(err).Warnf("ImageStatus: failed to stat snapshot %q in snapshotter %q; treating image %q as present", image.ChainID, snapshotterName, image.ID)
	}
	return true
}

// imageBlobsPresent reports whether every layer blob referenced by the image's
// manifest is currently in the content store. It is consulted only when
// discard_unpacked_layers is false, where blob presence is the expected steady
// state; a missing blob then indicates the corrupt state left by a prior
// discard_unpacked_layers=true run and the image must be re-pulled to restore
// it. The manifest is selected for platforms.Default(), matching both the
// chainID the CRI image store records and the platform unpackImage unpacks on
// the container-create path. The check is tolerant: if anything along the way
// (image lookup, manifest read, content store access) prevents a definitive
// answer, the image is treated as present so this check never turns into an
// availability regression for ImageStatus.
func (c *CRIImageService) imageBlobsPresent(ctx context.Context, image imagestore.Image) bool {
	if c.content == nil || c.images == nil {
		return true
	}
	if len(image.References) == 0 {
		return true
	}
	var (
		img containerdimages.Image
		err error
	)
	for _, ref := range image.References {
		img, err = c.images.Get(ctx, ref)
		if err == nil {
			break
		}
	}
	if err != nil {
		log.G(ctx).WithError(err).Debugf("ImageStatus: unable to look up image record for blob presence check; assuming present")
		return true
	}
	manifest, mErr := containerdimages.Manifest(ctx, c.content, img.Target, platforms.Default())
	if mErr != nil {
		log.G(ctx).WithError(mErr).Debugf("ImageStatus: unable to read manifest for %q; assuming blobs present", image.ID)
		return true
	}
	for _, layer := range manifest.Layers {
		if _, infoErr := c.content.Info(ctx, layer.Digest); infoErr != nil {
			if errdefs.IsNotFound(infoErr) {
				log.G(ctx).Debugf("ImageStatus: layer blob %q missing from content store for image %q; reporting as not present so it will be re-pulled", layer.Digest, image.ID)
				return false
			}
			log.G(ctx).WithError(infoErr).Warnf("ImageStatus: failed to stat blob %q; assuming present", layer.Digest)
		}
	}
	return true
}

// toCRIImage converts internal image object to CRI runtime.Image.
func toCRIImage(image imagestore.Image) *runtime.Image {
	repoTags, repoDigests := util.ParseImageReferences(image.References)
	runtimeImage := &runtime.Image{
		Id:          image.ID,
		RepoTags:    repoTags,
		RepoDigests: repoDigests,
		Size:        uint64(image.Size),
		Pinned:      image.Pinned,
	}
	uid, username := getUserFromImage(image.ImageSpec.Config.User)
	if uid != nil {
		runtimeImage.Uid = &runtime.Int64Value{Value: *uid}
	}
	runtimeImage.Username = username

	return runtimeImage
}

// getUserFromImage gets uid or user name of the image user.
// If user is numeric, it will be treated as uid; or else, it is treated as user name.
func getUserFromImage(user string) (*int64, string) {
	// return both empty if user is not specified in the image.
	if user == "" {
		return nil, ""
	}
	// split instances where the id may contain user:group
	user = strings.Split(user, ":")[0]
	// user could be either uid or user name. Try to interpret as numeric uid.
	uid, err := strconv.ParseInt(user, 10, 64)
	if err != nil {
		// If user is non numeric, assume it's user name.
		return nil, user
	}
	// If user is a numeric uid.
	return &uid, ""
}

// TODO (mikebrow): discuss moving this struct and / or constants for info map for some or all of these fields to CRI
type verboseImageInfo struct {
	ChainID   string          `json:"chainID"`
	ImageSpec imagespec.Image `json:"imageSpec"`
}

// toCRIImageInfo converts internal image object information to CRI image status response info map.
func (c *CRIImageService) toCRIImageInfo(ctx context.Context, image *imagestore.Image, verbose bool) (map[string]string, error) {
	if !verbose {
		return nil, nil
	}

	info := make(map[string]string)

	imi := &verboseImageInfo{
		ChainID:   image.ChainID,
		ImageSpec: image.ImageSpec,
	}

	m, err := json.Marshal(imi)
	if err == nil {
		info["info"] = string(m)
	} else {
		log.G(ctx).WithError(err).Errorf("failed to marshal info %v", imi)
		info["info"] = err.Error()
	}

	return info, nil
}
