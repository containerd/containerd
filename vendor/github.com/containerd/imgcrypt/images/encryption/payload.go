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
	"reflect"

	"github.com/containerd/containerd/diff"
	"github.com/gogo/protobuf/types"
)

var processorPayloadsUseGogo bool

func init() {
	var c = &diff.ApplyConfig{}
	var pbany *types.Any

	pp := reflect.TypeOf(c.ProcessorPayloads)
	processorPayloadsUseGogo = pp.Elem() == reflect.TypeOf(pbany)
}

func clearProcessorPayloads(c *diff.ApplyConfig) {
	var empty = reflect.MakeMap(reflect.TypeOf(c.ProcessorPayloads))
	reflect.ValueOf(&c.ProcessorPayloads).Elem().Set(empty)
}

func setProcessorPayload(c *diff.ApplyConfig, id string, value pbAny) {
	if c.ProcessorPayloads == nil {
		clearProcessorPayloads(c)
	}

	var v reflect.Value
	if processorPayloadsUseGogo {
		v = reflect.ValueOf(fromAny(value))
	} else {
		v = reflect.ValueOf(value)
	}
	reflect.ValueOf(c.ProcessorPayloads).SetMapIndex(reflect.ValueOf(id), v)
}
