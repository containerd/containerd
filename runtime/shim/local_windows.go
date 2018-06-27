// +build windows

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

package shim

import (
	"context"

	shimapi "github.com/containerd/containerd/runtime/shim/v1"
	ptypes "github.com/gogo/protobuf/types"
)

func (c *local) Delete(ctx context.Context, in *ptypes.Empty) (*shimapi.DeleteResponse, error) {
	return c.s.Delete(ctx, in)
}
