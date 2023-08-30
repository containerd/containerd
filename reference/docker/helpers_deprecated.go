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

package docker

import "github.com/distribution/reference"

// IsNameOnly returns true if reference only contains a repo name.
//
// Deprecated: use [reference.IsNameOnly].
func IsNameOnly(ref reference.Named) bool {
	return reference.IsNameOnly(ref)
}

// FamiliarName returns the familiar name string
// for the given named, familiarizing if needed.
//
// Deprecated: use [reference.FamiliarName].
func FamiliarName(ref reference.Named) string {
	return reference.FamiliarName(ref)
}

// FamiliarString returns the familiar string representation
// for the given reference, familiarizing if needed.
//
// Deprecated: use [reference.FamiliarString].
func FamiliarString(ref reference.Reference) string {
	return reference.FamiliarString(ref)
}

// FamiliarMatch reports whether ref matches the specified pattern.
// See [path.Match] for supported patterns.
//
// Deprecated: use [reference.FamiliarMatch].
func FamiliarMatch(pattern string, ref reference.Reference) (bool, error) {
	return reference.FamiliarMatch(pattern, ref)
}
