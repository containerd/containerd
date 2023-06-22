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

package log

// Often used fields for structured logging.
const (
	// Images/content
	ChainID   = "chainid"
	Name      = "name" // NOTE: Used in snapshotter contexts also.
	Image     = "image"
	Digest    = "digest"
	Host      = "host"
	MediaType = "mediatype"
	Ref       = "ref"
	Size      = "size"
	URL       = "url"

	// Containerd/services
	ID        = "id"
	Namespace = "namespace"
	Request   = "req"
	Stream    = "stream"

	// Snapshots
	Key         = "key" // NOTE: There's small uses of this in a non-snapshot context.
	Parent      = "parent"
	Snapshotter = "snapshotter"

	// CRI
	PodSandboxID = "podsandboxid"

	// Misc.
	Path = "path"
)
