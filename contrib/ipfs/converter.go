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
	"strings"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images/converter"
	"github.com/containerd/containerd/platforms"
	"github.com/ipfs/go-cid"
	files "github.com/ipfs/go-ipfs-files"
	iface "github.com/ipfs/interface-go-ipfs-core"
	"github.com/ipfs/interface-go-ipfs-core/options"
	ipath "github.com/ipfs/interface-go-ipfs-core/path"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Push pushes the provided image ref to IPFS with converting it to IPFS-enabled format.
func Push(ctx context.Context, client *containerd.Client, api iface.CoreAPI, ref string, layerConvert converter.ConvertFunc, platformMC platforms.MatchComparer) (ipath.Resolved, error) {
	ctx, done, err := client.WithLease(ctx)
	if err != nil {
		return nil, err
	}
	defer done(ctx)
	img, err := client.ImageService().Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	desc, err := converter.IndexConvertFuncWithHook(layerConvert, true, platformMC, converter.ConvertHooks{
		PostConvertHook: pushBlobHook(api),
	})(ctx, client.ContentStore(), img.Target)
	if err != nil {
		return nil, err
	}
	root, err := json.Marshal(desc)
	if err != nil {
		return nil, err
	}
	return api.Unixfs().Add(ctx, files.NewBytesFile(root), options.Unixfs.Pin(true), options.Unixfs.CidVersion(1))
}

func pushBlobHook(api iface.CoreAPI) converter.ConvertHookFunc {
	return func(ctx context.Context, cs content.Store, desc ocispec.Descriptor, newDesc *ocispec.Descriptor) (*ocispec.Descriptor, error) {
		resultDesc := newDesc
		if resultDesc == nil {
			descCopy := desc
			resultDesc = &descCopy
		}
		ra, err := cs.ReaderAt(ctx, *resultDesc)
		if err != nil {
			return nil, err
		}
		p, err := api.Unixfs().Add(ctx, files.NewReaderFile(content.NewReader(ra)), options.Unixfs.Pin(true), options.Unixfs.CidVersion(1))
		if err != nil {
			return nil, err
		}
		// record IPFS URL using CIDv1 : https://docs.ipfs.io/how-to/address-ipfs-on-web/#native-urls
		if p.Cid().Version() == 0 {
			return nil, fmt.Errorf("CID verions 0 isn't supported")
		}
		resultDesc.URLs = []string{"ipfs://" + p.Cid().String()}
		return resultDesc, nil
	}
}

func getPath(desc ocispec.Descriptor) (ipath.Path, error) {
	for _, u := range desc.URLs {
		if strings.HasPrefix(u, "ipfs://") {
			// support only content addressable URL (ipfs://<CID>)
			c, err := cid.Decode(u[7:])
			if err != nil {
				return nil, err
			}
			return ipath.IpfsPath(c), nil
		}
	}
	return nil, fmt.Errorf("no CID is recorded")
}
