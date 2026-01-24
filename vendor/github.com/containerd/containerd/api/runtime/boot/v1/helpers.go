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

package bootstrap

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// AddExtension adds a new extension to the BootstrapParams.
// The message is wrapped in a google.protobuf.Any with its type URL automatically set.
func (p *BootstrapParams) AddExtension(msg proto.Message) error {
	anyVal, err := anypb.New(msg)
	if err != nil {
		return err
	}

	p.Extensions = append(p.Extensions, &Extension{Value: anyVal})
	return nil
}

func GetExtension[T proto.Message](p *BootstrapParams) (T, error) {
	var (
		empty   T
		reflect = empty.ProtoReflect()
		name    = reflect.Descriptor().FullName()
		out     = reflect.New().Interface().(T)
	)

	for _, ext := range p.Extensions {
		if ext.GetValue().MessageIs(out) {
			if err := ext.GetValue().UnmarshalTo(out); err != nil {
				return out, fmt.Errorf("failed to unmarshal extension %q: %w", name, err)
			}

			return out, nil
		}
	}

	return out, fmt.Errorf("extension %q not found", name)
}
