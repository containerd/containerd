// +build windows

package windows

import (
	"io"
	"io/ioutil"
	"strings"
	"time"

	winio "github.com/Microsoft/go-winio"
	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/diff"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.DiffPlugin,
		ID:   "windows",
		Requires: []plugin.Type{
			plugin.MetadataPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			md, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}

			ic.Meta.Platforms = append(ic.Meta.Platforms, platforms.DefaultSpec())
			return NewWindowsDiff(md.(*metadata.DB).ContentStore())
		},
	})
}

type windowsDiff struct {
	store content.Store
}

var emptyDesc = ocispec.Descriptor{}

// NewWindowsDiff is the Windows container layer implementation of diff.Differ.
func NewWindowsDiff(store content.Store) (diff.Differ, error) {
	return &windowsDiff{
		store: store,
	}, nil
}

// Apply applies the content associated with the provided digests onto the
// provided mounts. Archive content will be extracted and decompressed if
// necessary.
func (s *windowsDiff) Apply(ctx context.Context, desc ocispec.Descriptor, mounts []mount.Mount) (d ocispec.Descriptor, err error) {
	t1 := time.Now()
	defer func() {
		if err == nil {
			log.G(ctx).WithFields(logrus.Fields{
				"d":     time.Now().Sub(t1),
				"dgst":  desc.Digest,
				"size":  desc.Size,
				"media": desc.MediaType,
			}).Debugf("diff applied")
		}
	}()
	var isCompressed bool
	switch desc.MediaType {
	case ocispec.MediaTypeImageLayer, images.MediaTypeDockerSchema2Layer:
	case ocispec.MediaTypeImageLayerGzip, images.MediaTypeDockerSchema2LayerGzip:
		isCompressed = true
	default:
		// Still apply all generic media types *.tar[.+]gzip and *.tar
		if strings.HasSuffix(desc.MediaType, ".tar.gzip") || strings.HasSuffix(desc.MediaType, ".tar+gzip") {
			isCompressed = true
		} else if !strings.HasSuffix(desc.MediaType, ".tar") {
			return emptyDesc, errors.Wrapf(errdefs.ErrNotImplemented, "unsupported diff media type: %v", desc.MediaType)
		}
	}

	ra, err := s.store.ReaderAt(ctx, desc.Digest)
	if err != nil {
		return emptyDesc, errors.Wrap(err, "failed to get reader from content store")
	}
	defer ra.Close()

	r := content.NewReader(ra)
	if isCompressed {
		ds, err := compression.DecompressStream(r)
		if err != nil {
			return emptyDesc, err
		}
		defer ds.Close()
		r = ds
	}

	digester := digest.Canonical.Digester()
	rc := &readCounter{
		r: io.TeeReader(r, digester.Hash()),
	}

	layer, parentLayerPaths, err := mountsToLayerAndParents(mounts)
	if err != nil {
		return emptyDesc, err
	}

	// TODO darrenstahlmsft: When this is done isolated, we should disable these.
	// it currently cannot be disabled, unless we add ref counting. Since this is
	// temporary, leaving it enabled is OK for now.
	if err := winio.EnableProcessPrivileges([]string{winio.SeBackupPrivilege, winio.SeRestorePrivilege}); err != nil {
		return emptyDesc, err
	}

	if _, err := archive.Apply(ctx, layer, rc, archive.WithParentLayers(parentLayerPaths), archive.AsWindowsContainerLayer()); err != nil {
		return emptyDesc, err
	}

	// Read any trailing data
	if _, err := io.Copy(ioutil.Discard, rc); err != nil {
		return emptyDesc, err
	}

	return ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Size:      rc.c,
		Digest:    digester.Digest(),
	}, nil
}

// DiffMounts creates a diff between the given mounts and uploads the result
// to the content store.
func (s *windowsDiff) DiffMounts(ctx context.Context, lower, upper []mount.Mount, opts ...diff.Opt) (d ocispec.Descriptor, err error) {
	panic("not implemented on Windows")
}

type readCounter struct {
	r io.Reader
	c int64
}

func (rc *readCounter) Read(p []byte) (n int, err error) {
	n, err = rc.r.Read(p)
	rc.c += int64(n)
	return
}

func mountsToLayerAndParents(mounts []mount.Mount) (string, []string, error) {
	if len(mounts) != 1 {
		return "", nil, errors.Wrap(errdefs.ErrInvalidArgument, "number of mounts should always be 1 for Windows layers")
	}
	layer := mounts[0].Source

	parentLayerPaths, err := mounts[0].GetParentPaths()
	if err != nil {
		return "", nil, err
	}

	return layer, parentLayerPaths, nil
}
