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

package transfer

import (
	"github.com/containerd/platforms"

	"github.com/containerd/containerd/v2/defaults"
)

func defaultUnpackConfig() []unpackConfiguration {
	spec := platforms.DefaultSpec()
	// default to Linux for unpacking as darwin images are not defined
	// and only linux is supported with the default snapshotter and differ.
	spec.OS = "linux"
	return []unpackConfiguration{
		{
			Platform:    platforms.Format(spec),
			Snapshotter: defaults.DefaultSnapshotter,
			Differ:      defaults.DefaultDiffer,
		},
	}
}
