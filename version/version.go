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

package version

import "runtime"

var (
	Name = "containerd"
	// Package is filled at linking time
	Package = "github.com/containerd/containerd/v2"

	// Version holds the complete version number. Filled in at linking time.
	Version = "2.2.0+unknown"

	// Revision is filled with the VCS (e.g. git) revision being used to build
	// the program at linking time.
	Revision = ""

	// GoVersion is Go tree's version.
	GoVersion = runtime.Version()

	// UserAgent is the default User-Agent header for http-requests such as
	// authenticating and pulling images from registries using the
	// core/remotes/docker package. This variable is filled in at linking
	// time, and users of containerd as a go module must set this variable
	// to a value matching their product, following the guidelines outlined
	// in [RFC9110, section 10.1.5], or to configure the User-Agent header
	// through other means.
	//
	// [RFC9110, section 10.1.5]: https://httpwg.org/specs/rfc9110.html#field.user-agent
	UserAgent = "containerd/2.0.0-rc.3+unknown"
)

// ConfigVersion is the current highest supported configuration version.
// This version is used by the main configuration as well as all plugins.
// Any configuration less than this version which has structural changes
// should migrate the configuration structures used by this version.
const ConfigVersion = 3
