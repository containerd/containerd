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

package ipfs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"

	"github.com/containerd/containerd/remotes"
	"github.com/ipfs/go-cid"
	files "github.com/ipfs/go-ipfs-files"
	iface "github.com/ipfs/interface-go-ipfs-core"
	ipath "github.com/ipfs/interface-go-ipfs-core/path"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type resolver struct {
	api    iface.CoreAPI
	scheme string
}

type ResolverOptions struct {
	// Scheme is the scheme to fetch the specified IPFS content. "ipfs" or "ipns".
	Scheme string
}

func NewResolver(client iface.CoreAPI, options ResolverOptions) (remotes.Resolver, error) {
	s := options.Scheme
	if s != "ipfs" && s != "ipns" {
		return nil, fmt.Errorf("unsupported scheme %q", s)
	}
	return &resolver{client, s}, nil
}

// Resolve resolves the provided ref for IPFS. ref must be a CID.
// TODO: Allow specifying IPFS path or URL. This requires to modify `reference` pkg because
//       it's incompatbile to the current reference specification.
func (r *resolver) Resolve(ctx context.Context, ref string) (name string, desc ocispec.Descriptor, err error) {
	c, err := cid.Decode(ref)
	if err != nil {
		return "", ocispec.Descriptor{}, err
	}
	p := ipath.New(path.Join("/", r.scheme, c.String()))
	if err := p.IsValid(); err != nil {
		return "", ocispec.Descriptor{}, err
	}
	n, err := r.api.Unixfs().Get(ctx, p)
	if err != nil {
		return "", ocispec.Descriptor{}, err
	}
	rc := files.ToFile(n)
	defer rc.Close()
	if err := json.NewDecoder(rc).Decode(&desc); err != nil {
		return "", ocispec.Descriptor{}, err
	}
	if _, err := getPath(desc); err != nil {
		return "", ocispec.Descriptor{}, err
	}
	return ref, desc, nil
}

func (r *resolver) Fetcher(ctx context.Context, ref string) (remotes.Fetcher, error) {
	return &fetcher{r}, nil
}

func (r *resolver) Pusher(ctx context.Context, ref string) (remotes.Pusher, error) {
	return nil, fmt.Errorf("immutable remote")
}

type fetcher struct {
	r *resolver
}

func (f *fetcher) Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	p, err := getPath(desc)
	if err != nil {
		return nil, err
	}
	n, err := f.r.api.Unixfs().Get(ctx, p)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get file %q", p.String())
	}
	return files.ToFile(n), nil
}
