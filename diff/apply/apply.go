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
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/diff"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/log"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// NewFileSystemApplier returns an applier which simply mounts
// and applies diff onto the mounted filesystem.
func NewFileSystemApplier(cs content.Provider) diff.Applier {
	return &fsApplier{
		store: cs,
	}
}

type fsApplier struct {
	store content.Provider
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

	ra, err := s.store.ReaderAt(ctx, desc)
	if err != nil {
		return emptyDesc, fmt.Errorf("failed to get reader from content store: %w", err)
	}
	defer ra.Close()

	//TODO (ikema): Change this into a processor
	// Validate the .unpack file format
	// Line 1: sha256sum
	// Line 2: Size
	// Line 3+: diff list
	scanner := bufio.NewScanner(content.NewReader(ra))
	var sha256sum string
	if scanner.Scan() {
		sha256sum = scanner.Text()
		sha256sum = strings.Split(sha256sum, " ")[0]
	} else {
		return emptyDesc, fmt.Errorf("no sha256 in .unpack")
	}
	var tarSize int
	if scanner.Scan() {
		tarSize, err = strconv.Atoi(scanner.Text())
		if err != nil {
			return emptyDesc, err
		}
	} else {
		return emptyDesc, fmt.Errorf("no tarSize in .unpack")
	}

	rb, err := s.store.ReaderAt(ctx, desc)
	if err != nil {
		return emptyDesc, fmt.Errorf("failed to get reader from content store: %w", err)
	}
	defer rb.Close()

	if err := applyPreunpacked(ctx, mounts, content.NewReader(rb), desc); err != nil {
		return emptyDesc, err
	}

	return ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Size:      int64(tarSize),
		Digest:    digest.NewDigestFromEncoded(digest.SHA256, sha256sum),
	}, nil

	/*
		var processors []diff.StreamProcessor
		processor := diff.NewProcessorChain(desc.MediaType, content.NewReader(ra))
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

		if err := apply(ctx, mounts, rc); err != nil {
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
	*/
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
