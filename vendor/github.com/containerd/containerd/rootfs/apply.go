package rootfs

import (
	"fmt"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshot"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

type Applier interface {
	Apply(context.Context, ocispec.Descriptor, []mount.Mount) (ocispec.Descriptor, error)
}

type Layer struct {
	Diff ocispec.Descriptor
	Blob ocispec.Descriptor
}

func ApplyLayers(ctx context.Context, layers []Layer, sn snapshot.Snapshotter, a Applier) (digest.Digest, error) {
	var chain []digest.Digest
	for _, layer := range layers {
		if err := applyLayer(ctx, layer, chain, sn, a); err != nil {
			// TODO: possibly wait and retry if extraction of same chain id was in progress
			return "", err
		}

		chain = append(chain, layer.Diff.Digest)
	}
	return identity.ChainID(chain), nil
}

func applyLayer(ctx context.Context, layer Layer, chain []digest.Digest, sn snapshot.Snapshotter, a Applier) error {
	var (
		parent  = identity.ChainID(chain)
		chainID = identity.ChainID(append(chain, layer.Diff.Digest))
		diff    ocispec.Descriptor
	)

	_, err := sn.Stat(ctx, chainID.String())
	if err == nil {
		log.G(ctx).Debugf("Extraction not needed, layer snapshot exists")
		return nil
	} else if !snapshot.IsNotExist(err) {
		return errors.Wrap(err, "failed to stat snapshot")
	}

	key := fmt.Sprintf("extract %s", chainID)

	// Prepare snapshot with from parent
	mounts, err := sn.Prepare(ctx, key, parent.String())
	if err != nil {
		//TODO: If is snapshot exists error, retry
		return errors.Wrap(err, "failed to prepare extraction layer")
	}
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).WithField("key", key).Infof("Apply failure, attempting cleanup")
			if rerr := sn.Remove(ctx, key); rerr != nil {
				log.G(ctx).WithError(rerr).Warnf("Extraction snapshot %q removal failed: %v", key)
			}
		}
	}()

	diff, err = a.Apply(ctx, layer.Blob, mounts)
	if err != nil {
		return errors.Wrapf(err, "failed to extract layer %s", layer.Diff.Digest)
	}
	if diff.Digest != layer.Diff.Digest {
		err = errors.Errorf("wrong diff id calculated on extraction %q", diff.Digest)
		return err
	}

	if err = sn.Commit(ctx, chainID.String(), key); err != nil {
		return errors.Wrapf(err, "failed to commit snapshot %s", parent)
	}

	return nil
}
