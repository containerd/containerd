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

package transfer

import (
	"context"
	"io"

	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/pkg/unpack"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type Transferer interface {
	Transfer(context.Context, interface{}, interface{}, ...Opt) error
}

type ImageResolver interface {
	Resolve(ctx context.Context) (name string, desc ocispec.Descriptor, err error)

	Fetcher(ctx context.Context, ref string) (Fetcher, error)
}

type Fetcher interface {
	Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error)
}

// ImageFilterer is used to filter out child objects of an image
type ImageFilterer interface {
	ImageFilter(images.HandlerFunc) images.HandlerFunc
}

type ImageStorer interface {
	Store(context.Context, ocispec.Descriptor) (images.Image, error)
}

type ImageUnpacker interface {
	// TODO: Or unpack options?
	UnpackPlatforms() []unpack.Platform
}

type TransferOpts struct {
}

type Opt func(*TransferOpts)

func WithProgress() Opt {
	return nil
}

type Progress struct {
	Event    string
	Name     string
	Digest   string
	Progress int64
	Total    int64
}

/*
// Distribution options
// Stream handler
// Progress rate
// Unpack options
// Remote options
// Cases:
//  Registry -> Content/ImageStore (pull)
//  Registry -> Registry
//  Content/ImageStore -> Registry (push)
//  Content/ImageStore -> Content/ImageStore (tag)
// Common fetch/push interface for registry, content/imagestore, OCI index
// Always starts with string for source and destination, on client side, does not need to resolve
//  Higher level implementation just takes strings and options
//  Lower level implementation takes pusher/fetcher?

*/
