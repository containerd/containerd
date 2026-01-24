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
// If the message is already an *anypb.Any, it is used directly without double-wrapping.
func (p *BootstrapParams) AddExtension(msg proto.Message) error {
	var anyVal *anypb.Any
	if a, ok := msg.(*anypb.Any); ok {
		// Already an Any, use it directly
		anyVal = a
	} else {
		var err error
		anyVal, err = anypb.New(msg)
		if err != nil {
			return err
		}
	}

	p.Extensions = append(p.Extensions, &Extension{Value: anyVal})
	return nil
}

// FindExtension finds an extension matching the type of dst and unmarshals it.
// Returns true if found, false if not found.
func (p *BootstrapParams) FindExtension(dst proto.Message) error {
	name := dst.ProtoReflect().Descriptor().FullName()

	for _, ext := range p.Extensions {
		if ext.GetValue().MessageIs(dst) {
			if err := ext.GetValue().UnmarshalTo(dst); err != nil {
				return fmt.Errorf("failed to unmarshal extension %q: %w", name, err)
			}
			return nil
		}
	}

	return nil
}
