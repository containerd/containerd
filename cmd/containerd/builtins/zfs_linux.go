//go:build !no_zfs

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

package builtins

// zfs snapshotter is temporarily removed until it is updated to use the
// new plugin package. In the future, the external plugin package will
// make it easier to update zfs and containerd independently without
// the dependency loop. Add back before 2.0 release.
//import _ "github.com/containerd/zfs/plugin"
