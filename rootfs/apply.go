package rootfs

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/containerd/containerd/diff"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshot"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

// Layer represents the descriptors for a layer diff. These descriptions
// include the descriptor for the uncompressed tar diff as well as a blob
// used to transport that tar. The blob descriptor may or may not describe
// a compressed object.
type Layer struct {
	Diff ocispec.Descriptor
	Blob ocispec.Descriptor
}

// ApplyLayer applies a single layer on top of the given provided layer chain,
// using the provided snapshotter and applier. If the layer was unpacked true
// is returned, if the layer already exists false is returned.
func ApplyLayer(ctx context.Context, layer Layer, chain []digest.Digest, sn snapshot.Snapshotter, a diff.Differ, opts ...snapshot.Opt) (bool, error) {
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

	key := fmt.Sprintf("extract-%s %s", uniquePart(), chainID)

	// Prepare snapshot with from parent, label as root
	mounts, err := sn.Prepare(ctx, key, parent.String(), opts...)
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

	if err = sn.Commit(ctx, chainID.String(), key, opts...); err != nil {
		if !errdefs.IsAlreadyExists(err) {
			return false, errors.Wrapf(err, "failed to commit snapshot %s", parent)
		}

		// Destination already exists, cleanup key and return without error
		err = nil
		if err := sn.Remove(ctx, key); err != nil {
			return false, errors.Wrapf(err, "failed to cleanup aborted apply %s", key)
		}
		return false, nil
	}

	return true, nil
}

func uniquePart() string {
	t := time.Now()
	var b [3]byte
	// Ignore read failures, just decreases uniqueness
	rand.Read(b[:])
	return fmt.Sprintf("%d-%s", t.Nanosecond(), base64.URLEncoding.EncodeToString(b[:]))
}
