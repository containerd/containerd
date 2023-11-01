//go:build (!amd64 && !arm64) || static_build || gccgo

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

package dynamic

// loadPlugins is not supported;
//
// - with gccgo: gccgo has no plugin support golang/go#36403
// - on static builds; https://github.com/containerd/containerd/commit/0d682e24a1ba8e93e5e54a73d64f7d256f87492f
// - on architectures other than amd64 and arm64 (other architectures need to be tested)
func loadPlugins(path string) (int, error) {
	return 0, nil
}
