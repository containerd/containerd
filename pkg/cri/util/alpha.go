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

package util

import (
	"github.com/containerd/containerd/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func AlphaReqToV1Req(
	alphar protoreflect.ProtoMessage,
	v1r interface{ Unmarshal(_ []byte) error },
) error {
	p, err := proto.Marshal(alphar)
	if err != nil {
		return err
	}

	if err = v1r.Unmarshal(p); err != nil {
		return err
	}
	return nil
}

func V1RespToAlphaResp(
	v1res interface{ Marshal() ([]byte, error) },
	alphares protoreflect.ProtoMessage,
) error {
	p, err := v1res.Marshal()
	if err != nil {
		return err
	}

	if err = proto.Unmarshal(p, alphares); err != nil {
		return err
	}
	return nil
}
