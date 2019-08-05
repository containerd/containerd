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

package utils

import (
	"github.com/containerd/containerd/images/encryption"
	"github.com/containerd/typeurl"
	"github.com/gogo/protobuf/proto"
	"github.com/gogo/protobuf/types"
	"github.com/pkg/errors"
)

func init() {
	typeurl.Register(&encryption.LayerToolDecryptData{}, "LayerToolDecryptData")
}

// UnmarshalLayerToolDecryptData unmarshals a byte array to LayerToolDecryptData
func UnmarshalLayerToolDecryptData(decryptData []byte) (*encryption.LayerToolDecryptData, error) {
	var pb types.Any

	if err := proto.Unmarshal(decryptData, &pb); err != nil {
		return nil, errors.Wrapf(err, "could not proto.Unmarshal() decrypt data")
	}
	any, err := typeurl.UnmarshalAny(&pb)
	if err != nil {
		return nil, errors.Wrapf(err, "could not UnmarshalAny() the decrypt data")
	}
	url, err := typeurl.TypeURL(any)
	if err != nil {
		return nil, errors.Wrapf(err, "call to TypeUrl(any) failed")
	}

	var ltdd *encryption.LayerToolDecryptData

	switch url {
	case "LayerToolDecryptData":
		ltdd, _ = any.(*encryption.LayerToolDecryptData)
	default:
		return nil, errors.Errorf("received an unknown data type '%s'", url)
	}
	return ltdd, nil
}
