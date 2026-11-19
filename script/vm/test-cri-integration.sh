#!/usr/bin/env bash

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

#
# Runs the CRI integration tests inside a virtual machine
# provisioned with provision.sh.
#
set -eux -o pipefail

if [[ "$(id -u)" != "0" ]]; then
	echo "must be executed as the root user" >&2
	exit 1
fi

script_dir="$(cd -- "$(dirname -- "$0")" > /dev/null 2>&1; pwd -P)"
containerd_dir="$(cd -- "${script_dir}/../.." > /dev/null 2>&1; pwd -P)"

export CGROUP_DRIVER="${CGROUP_DRIVER:-}"
export RUNC_FLAVOR="${RUNC_FLAVOR:-runc}"
export GOTEST="${GOTEST:-go test}"
export GITHUB_WORKSPACE="${GITHUB_WORKSPACE:-}"

export GOPATH="${GOPATH:-/go}"
export PATH="/usr/local/go/bin:${GOPATH}/bin:/usr/local/bin:/usr/local/sbin:${PATH}"

cleanup() {
	rm -rf /var/lib/containerd* /run/containerd* /tmp/containerd* /tmp/test* /tmp/failpoint* /tmp/nri*
}

cleanup
cd "${containerd_dir}"
# cri-integration.sh executes containerd from ./bin, not from $PATH .
make BUILDTAGS="seccomp selinux no_btrfs no_devmapper no_zfs" binaries bin/cri-integration.test
chcon -v -t container_runtime_exec_t ./bin/{containerd,containerd-shim*}
CONTAINERD_RUNTIME=io.containerd.runc.v2 ./script/test/cri-integration.sh
cleanup
