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

package diff

// On Linux, prefer the overlay differ first. It uses overlayfs's native
// upperdir tracking to walk only changed files, avoiding a full scan of the
// lower layers. It returns ErrNotImplemented for non-overlay mounts and the
// service falls back automatically to the walking differ.
var defaultDifferConfig = &config{
	Order:  []string{"overlay", "walking"},
	SyncFs: false,
}
