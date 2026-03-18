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

package apply

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// NewFileSystemApplier returns an applier which simply mounts
// and applies diff onto the mounted filesystem.
func NewFileSystemApplier(cs content.Provider, opts ...FileSystemApplierOpt) diff.Applier {
	config := &fsApplierConfig{
		applyFunc: apply,
		parallel:  false,
	}
	for _, o := range opts {
		if err := o(config); err != nil {
			return nil
		}
	}
	return &fsApplier{
		store:    cs,
		apply:    config.applyFunc,
		parallel: config.parallel,
	}
}

// FileSystemApplierOpt is used to configure filesystem applier.
type FileSystemApplierOpt func(*fsApplierConfig) error

// FileSystemApply is a function type that defines the signature for
// applying a reader content to a set of filesystem mounts. It allows
// for customized mount/apply logic for different filesystems.
type FileSystemApply func(context.Context, []mount.Mount, io.Reader, bool) error

// WithCustomApplyFunc allows callers to customize the apply function
func WithCustomApplyFunc(f FileSystemApply) FileSystemApplierOpt {
	return func(c *fsApplierConfig) error {
		c.applyFunc = f
		return nil
	}
}

// WithParallelSupport signifies that the applier supports parallel application of diffs
// fsaApplier will return ErrNotImplemented error if this option is not set, but
// the caller requires parallel application of diffs.
func WithParallelSupport(parallel bool) FileSystemApplierOpt {
	return func(c *fsApplierConfig) error {
		c.parallel = parallel
		return nil
	}
}

type fsApplier struct {
	store    content.Provider
	apply    FileSystemApply
	parallel bool
}

type fsApplierConfig struct {
	applyFunc FileSystemApply
	parallel  bool
}

var emptyDesc = ocispec.Descriptor{}

// Apply applies the content associated with the provided digests onto the
// provided mounts. Archive content will be extracted and decompressed if
// necessary.
func (s *fsApplier) Apply(ctx context.Context, desc ocispec.Descriptor, mounts []mount.Mount, opts ...diff.ApplyOpt) (d ocispec.Descriptor, err error) {
	t1 := time.Now()
	defer func() {
		if err == nil {
			log.G(ctx).WithFields(log.Fields{
				"d":      time.Since(t1),
				"digest": desc.Digest,
				"size":   desc.Size,
				"media":  desc.MediaType,
			}).Debugf("diff applied")
		}
	}()

	var config diff.ApplyConfig
	for _, o := range opts {
		if err := o(ctx, desc, &config); err != nil {
			return emptyDesc, fmt.Errorf("failed to apply config opt: %w", err)
		}
	}

	// ErrNotImplemented must be returned if the applier does not support parallel apply.
	// This error tells the diff service to call the next applier in the ordered list.
	if config.Parallel && !s.parallel {
		return emptyDesc, fmt.Errorf("parallel application of diffs is not supported by this applier: %w", errdefs.ErrNotImplemented)
	}

	ra, err := s.store.ReaderAt(ctx, desc)
	if err != nil {
		return emptyDesc, fmt.Errorf("failed to get reader from content store: %w", err)
	}
	var r io.ReadCloser
	if config.Progress != nil {
		r = newProgressReader(ra, config.Progress)
	} else {
		r = newReadCloser(ra)
	}
	defer r.Close()

	var processors []diff.StreamProcessor
	processor := diff.NewProcessorChain(desc.MediaType, r)
	processors = append(processors, processor)
	for {
		if processor, err = diff.GetProcessor(ctx, processor, config.ProcessorPayloads); err != nil {
			return emptyDesc, fmt.Errorf("failed to get stream processor for %s: %w", desc.MediaType, err)
		}
		processors = append(processors, processor)
		if processor.MediaType() == ocispec.MediaTypeImageLayer {
			break
		}
	}
	defer processor.Close()

	digester := digest.Canonical.Digester()
	rc := &readCounter{
		r: io.TeeReader(processor, digester.Hash()),
	}

	if err := s.apply(ctx, mounts, rc, config.SyncFs); err != nil {
		return emptyDesc, err
	}

	// Read any trailing data
	if _, err := io.Copy(io.Discard, rc); err != nil {
		return emptyDesc, err
	}

	for _, p := range processors {
		if ep, ok := p.(interface {
			Err() error
		}); ok {
			if err := ep.Err(); err != nil {
				return emptyDesc, err
			}
		}
	}

	return ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Size:      rc.c,
		Digest:    digester.Digest(),
	}, nil
}

type readCounter struct {
	r io.Reader
	c int64
}

func (rc *readCounter) Read(p []byte) (n int, err error) {
	n, err = rc.r.Read(p)
	if n > 0 {
		rc.c += int64(n)
	}
	return
}

type progressReader struct {
	rc *readCounter
	c  io.Closer
	p  func(int64)
}

func newProgressReader(ra content.ReaderAt, p func(int64)) io.ReadCloser {
	return &progressReader{
		rc: &readCounter{
			r: content.NewReader(ra),
			c: 0,
		},
		c: ra,
		p: p,
	}
}

func (pr *progressReader) Read(p []byte) (n int, err error) {
	// Call the progress function with the current count, indicating
	// the previously read content has been processed. Initial
	// progress of 0 indicates start of processing.
	pr.p(pr.rc.c)
	n, err = pr.rc.Read(p)
	return
}

func (pr *progressReader) Close() error {
	pr.p(pr.rc.c)
	return pr.c.Close()
}

type readCloser struct {
	io.Reader
	io.Closer
}

func newReadCloser(ra content.ReaderAt) io.ReadCloser {
	return &readCloser{
		Reader: content.NewReader(ra),
		Closer: ra,
	}
}
