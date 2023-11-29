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

package labels

const (
	// LabelUncompressed is added to compressed layer contents.
	// The value is digest of the uncompressed content.
	LabelUncompressed = "containerd.io/uncompressed"

	// LabelSharedNamespace is added to a namespace to allow that namespaces
	// contents to be shared.
	LabelSharedNamespace = "containerd.io/namespace.shareable"

	// LabelDistributionSource is added to content to indicate its origin.
	// e.g., "containerd.io/distribution.source.docker.io=library/redis"
	LabelDistributionSource = "containerd.io/distribution.source"

	// RuntimeHandlerLabelPrefix is common prefix for runtime handler used for image pull
	RuntimeHandlerLabelPrefix = "containerd.io/imagePullRuntimeHandler"

	RuntimeHandlerLabelFormat = "%s.%s"
)
