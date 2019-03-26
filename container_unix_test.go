// +build !windows

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

package containerd

import context "context"

// newContainerWithOpts is a test wrapper so that OS-specific options are
// passed to the runtime. No-op on Unix platforms.
func (c *Client) newContainerWithOpts(ctx context.Context, id string, opts ...NewContainerOpts) (Container, error) {
	return c.NewContainer(ctx, id, opts...)
}
