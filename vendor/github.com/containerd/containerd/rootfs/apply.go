package rootfs

import (
	"fmt"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshot"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

// Applier is used to apply a descriptor of a layer diff on top of mounts.
type Applier interface {
	Apply(context.Context, ocispec.Descriptor, []mount.Mount) (ocispec.Descriptor, error)
}

// Layer represents the descriptors for a layer diff. These descriptions
// include the descriptor for the uncompressed tar diff as well as a blob
// used to transport that tar. The blob descriptor may or may not describe
// a compressed object.
type Layer struct {
	Diff ocispec.Descriptor
	Blob ocispec.Descriptor
}

// ApplyLayers applies all the layers using the given snapshotter and applier.
// The returned result is a chain id digest representing all the applied layers.
// Layers are applied in order they are given, making the first layer the
// bottom-most layer in the layer chain.
func ApplyLayers(ctx context.Context, layers []Layer, sn snapshot.Snapshotter, a Applier) (digest.Digest, error) {
	var chain []digest.Digest
	for _, layer := range layers {
		if _, err := ApplyLayer(ctx, layer, chain, sn, a); err != nil {
			// TODO: possibly wait and retry if extraction of same chain id was in progress
			return "", err
		}

		chain = append(chain, layer.Diff.Digest)
	}
	return identity.ChainID(chain), nil
}

// ApplyLayer applies a single layer on top of the given provided layer chain,
// using the provided snapshotter and applier. If the layer was unpacked true
// is returned, if the layer already exists false is returned.
func ApplyLayer(ctx context.Context, layer Layer, chain []digest.Digest, sn snapshot.Snapshotter, a Applier) (bool, error) {
	var (
		parent  = identity.ChainID(chain)
		chainID = identity.ChainID(append(chain, layer.Diff.Digest))
		diff    ocispec.Descriptor
	)

	_, err := sn.Stat(ctx, chainID.String())
	if err == nil {
		log.G(ctx).Debugf("Extraction not needed, layer snapshot exists")
		return false, nil
	} else if !errdefs.IsNotFound(err) {
		return false, errors.Wrap(err, "failed to stat snapshot")
	}

	key := fmt.Sprintf("extract %s", chainID)

	// Prepare snapshot with from parent
	mounts, err := sn.Prepare(ctx, key, parent.String())
	if err != nil {
		//TODO: If is snapshot exists error, retry
		return false, errors.Wrap(err, "failed to prepare extraction layer")
	}
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).WithField("key", key).Infof("Apply failure, attempting cleanup")
			if rerr := sn.Remove(ctx, key); rerr != nil {
				log.G(ctx).WithError(rerr).Warnf("Extraction snapshot %q removal failed", key)
			}
		}
	}()

	diff, err = a.Apply(ctx, layer.Blob, mounts)
	if err != nil {
		return false, errors.Wrapf(err, "failed to extract layer %s", layer.Diff.Digest)
	}
	if diff.Digest != layer.Diff.Digest {
		err = errors.Errorf("wrong diff id calculated on extraction %q", diff.Digest)
		return false, err
	}

	if err = sn.Commit(ctx, chainID.String(), key); err != nil {
		return false, errors.Wrapf(err, "failed to commit snapshot %s", parent)
	}

	return true, nil
}
