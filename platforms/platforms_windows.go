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

package platforms

import (
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

// newDefaultMatcher returns an instance of the Windows matcher for the proived platform specs.
//
// If the reference platform is pre-RS5 (< 18.09), the default matcher compares a given
// platforms' build number so it exactly matches the expected build number, alongside
// the default OS type/architecture checks.
//
// If comparing against RS5+, which should be able to run any newer/older Windows images under
// Hyper-V isolation, only the OS type/architecture are checked.
//
// https://learn.microsoft.com/en-us/virtualization/windowscontainers/deploy-containers/version-compatibility#windows-server-host-os-compatibility

func newDefaultMatcher(platform specs.Platform) Matcher {
	return defaultWindowsMatcher{
		innerPlatform: platform,
		defaultMatcher: &matcher{
			Platform: Normalize(platform),
		},
	}
}
