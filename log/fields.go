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
	ChainID = "chainid"
	Digest  = "digest"
	Ref     = "ref"
	URL     = "url"

	// Transfer
	Stream = "stream"

	// Containerd
	ID        = "id"
	Namespace = "namespace"
	Request   = "req"

	// Snapshots
	Snapshotter = "snapshotter"
	Path        = "path"
	Parent      = "parent"
	Key         = "key"
	Name        = "name"

	// CRI
	PodSandboxID = "podsandboxid"
)
