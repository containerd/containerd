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

package fuzz

import (
	// base containerd imports
	_ "github.com/containerd/containerd/v2/diff/walking/plugin"
	_ "github.com/containerd/containerd/v2/events/plugin"
	_ "github.com/containerd/containerd/v2/gc/scheduler"
	_ "github.com/containerd/containerd/v2/leases/plugin"
	_ "github.com/containerd/containerd/v2/metadata/plugin"
	_ "github.com/containerd/containerd/v2/pkg/cri"
	_ "github.com/containerd/containerd/v2/pkg/nri/plugin"
	_ "github.com/containerd/containerd/v2/plugins/cri/images"
	_ "github.com/containerd/containerd/v2/plugins/imageverifier"
	_ "github.com/containerd/containerd/v2/plugins/sandbox"
	_ "github.com/containerd/containerd/v2/plugins/streaming"
	_ "github.com/containerd/containerd/v2/plugins/transfer"
	_ "github.com/containerd/containerd/v2/runtime/restart/monitor"
	_ "github.com/containerd/containerd/v2/runtime/v2"
	_ "github.com/containerd/containerd/v2/services/containers"
	_ "github.com/containerd/containerd/v2/services/content"
	_ "github.com/containerd/containerd/v2/services/diff"
	_ "github.com/containerd/containerd/v2/services/events"
	_ "github.com/containerd/containerd/v2/services/healthcheck"
	_ "github.com/containerd/containerd/v2/services/images"
	_ "github.com/containerd/containerd/v2/services/introspection"
	_ "github.com/containerd/containerd/v2/services/leases"
	_ "github.com/containerd/containerd/v2/services/namespaces"
	_ "github.com/containerd/containerd/v2/services/opt"
	_ "github.com/containerd/containerd/v2/services/sandbox"
	_ "github.com/containerd/containerd/v2/services/snapshots"
	_ "github.com/containerd/containerd/v2/services/streaming"
	_ "github.com/containerd/containerd/v2/services/tasks"
	_ "github.com/containerd/containerd/v2/services/transfer"
	_ "github.com/containerd/containerd/v2/services/version"
)
