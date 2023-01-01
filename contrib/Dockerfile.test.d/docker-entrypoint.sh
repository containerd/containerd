#!/bin/bash

#   Copyright The containerd Authors.

#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at

#       http://www.apache.org/licenses/LICENSE-2.0

#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.

set -eu -o pipefail

if [ -f "/sys/fs/cgroup/cgroup.controllers" ]; then
	echo >&2 "Enabling cgroup v2 nesting"
	# https://github.com/moby/moby/blob/v20.10.7/hack/dind#L28-L38
	mkdir -p /sys/fs/cgroup/init
	xargs -rn1 </sys/fs/cgroup/cgroup.procs >/sys/fs/cgroup/init/cgroup.procs || :
	sed -e 's/ / +/g' -e 's/^/+/' </sys/fs/cgroup/cgroup.controllers \
		>/sys/fs/cgroup/cgroup.subtree_control
fi

exec "$@"
