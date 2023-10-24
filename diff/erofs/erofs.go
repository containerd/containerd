//go:build linux

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

package erofs

import (
	"context"
	"fmt"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/diff"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/log"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	"io"
	"os/exec"
	"path/filepath"
	"time"
)

type MediaHandler struct {
	BinaryName string   `toml:"binary_name"`
	Args       []string `toml:"args"`
}

// Config represents configuration for the overlay plugin.
type Config struct {
	// Root directory for the plugin
	MediaHandlers map[string]MediaHandler `toml:"media_handlers"`
}

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.DiffPlugin,
		ID:   "erofs",
		Requires: []plugin.Type{
			plugin.MetadataPlugin,
		},
		Config: &Config{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			md, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}

			ic.Meta.Platforms = append(ic.Meta.Platforms, platforms.DefaultSpec())
			cs := md.(*metadata.DB).ContentStore()
			config := ic.Config.(*Config)
			return NewErofsDiff(cs, config.MediaHandlers)
		},
	})
}

// CompareApplier handles both comparison and
// application of layer diffs.
type CompareApplier interface {
	diff.Applier
	diff.Comparer
}

// erofsDiff does erofs comparison and application
type erofsDiff struct {
	store         content.Store
	globalHandler map[string]MediaHandler
}

var emptyDesc = ocispec.Descriptor{}

// NewErofsDiff is the erofs container layer implementation
func NewErofsDiff(store content.Store, handlers map[string]MediaHandler) (CompareApplier, error) {
	return erofsDiff{
		store:         store,
		globalHandler: handlers,
	}, nil
}

// Apply applies the content associated with the provided digests onto the
// provided mounts. Archive content will be extracted and decompressed if
// necessary.
func (s erofsDiff) Apply(ctx context.Context, desc ocispec.Descriptor, mounts []mount.Mount, opts ...diff.ApplyOpt) (d ocispec.Descriptor, err error) {
	t1 := time.Now()
	defer func() {
		if err == nil {
			log.G(ctx).WithFields(logrus.Fields{
				"d":      time.Since(t1),
				"digest": desc.Digest,
				"size":   desc.Size,
				"media":  desc.MediaType,
			}).Debugf("diff applied")
		}
	}()

	mediaHandler, ok := s.globalHandler[desc.MediaType]
	if !ok {
		return emptyDesc, fmt.Errorf("currently unsupported media type: %s", desc.MediaType)
	}

	var config diff.ApplyConfig
	for _, o := range opts {
		if err := o(ctx, desc, &config); err != nil {
			return emptyDesc, fmt.Errorf("failed to apply config opt: %w", err)
		}
	}

	ra, err := s.store.ReaderAt(ctx, desc)
	if err != nil {
		return emptyDesc, fmt.Errorf("failed to get reader from content store: %w", err)
	}
	defer ra.Close()

	rc := &readCounter{
		r: content.NewReader(ra),
	}

	if err := apply(ctx, mounts, rc, mediaHandler); err != nil {
		return emptyDesc, err
	}

	log.G(ctx).Infof("read %v from content", rc.c)
	// Read any trailing data
	if _, err := io.Copy(io.Discard, rc); err != nil {
		return emptyDesc, err
	}

	return ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Size:      rc.c,
		Digest:    desc.Digest,
	}, nil
}

// Compare creates a diff between the given mounts and uploads the result
// to the content store.
func (s erofsDiff) Compare(ctx context.Context, lower, upper []mount.Mount, opts ...diff.Opt) (d ocispec.Descriptor, err error) {
	return emptyDesc, fmt.Errorf("erofsDiff does not implement Compare method: %w", errdefs.ErrNotImplemented)
}

func apply(ctx context.Context, mounts []mount.Mount, r io.Reader, handler MediaHandler) error {
	return mount.WithTempMount(ctx, mounts, func(root string) error {
		cmd := exec.CommandContext(ctx, handler.BinaryName, handler.Args...)
		cmd.Args = append(cmd.Args, filepath.Join(root, "layer.erofs"))
		cmd.Stdin = r
		out, err := cmd.CombinedOutput()
		if err != nil {
			return err
		}
		log.G(ctx).Infof("running %s %s %v", cmd.Path, cmd.Args, string(out))
		return nil
	})
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
