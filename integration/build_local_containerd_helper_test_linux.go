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

package integration

import (
	// Register for linux platforms
	_ "github.com/containerd/containerd/v2/plugins/imageverifier"    // WithInMemoryServices will fail otherwise
	_ "github.com/containerd/containerd/v2/plugins/sandbox"          // WithInMemoryServices will fail otherwise
	_ "github.com/containerd/containerd/v2/plugins/services/sandbox" // WithInMemoryServices will fail otherwise
	_ "github.com/containerd/containerd/v2/plugins/snapshots/overlay/plugin"
	_ "github.com/containerd/containerd/v2/plugins/streaming" // WithInMemoryServices will fail otherwise
)
