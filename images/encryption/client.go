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

package encryption

import (
	"context"

	"github.com/containerd/containerd/diff"
	encconfig "github.com/containerd/containerd/pkg/encryption/config"
	"github.com/containerd/typeurl"
	"github.com/gogo/protobuf/types"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// LayerToolDecryptData holds data that the external layer decryption tool
// needs for decrypting a layer
type LayerToolDecryptData struct {
	DecryptConfig encconfig.DecryptConfig
	Descriptor    ocispec.Descriptor
}

func init() {
	typeurl.Register(&LayerToolDecryptData{}, "LayerToolDecryptData")
}

// WithDecryptedUnpack allows to pass parameters the 'layertool' needs to the applier
func WithDecryptedUnpack(data *LayerToolDecryptData) diff.ApplyOpt {
	return func(_ context.Context, desc ocispec.Descriptor, c *diff.ApplyConfig) error {
		if c.ProcessorPayloads == nil {
			c.ProcessorPayloads = make(map[string]*types.Any)
		}
		data.Descriptor = desc
		any, err := typeurl.MarshalAny(data)
		if err != nil {
			return errors.Wrapf(err, "failed to typeurl.MarshalAny(LayerToolDecryptData)")
		}

		c.ProcessorPayloads["io.containerd.layertool.tar"] = any
		c.ProcessorPayloads["io.containerd.layertool.tar.gzip"] = any

		return nil
	}
}
