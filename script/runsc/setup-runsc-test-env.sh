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
# Provisions the local machine to run the integration-runsc CI job locally.
# After running this setup script, use run-runsc-tests.sh to execute the test suite.
#
# Usage: sudo CGROUP_DRIVER=systemd script/runsc/setup-runsc-test-env.sh
#
set -eux -o pipefail

if [[ "$(id -u)" != "0" ]]; then
	echo "must be run as root (use sudo)" >&2
	exit 1
fi

: "${CGROUP_DRIVER:=systemd}"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" > /dev/null 2>&1; pwd -P)"
setup_dir="$(cd -- "${script_dir}/../setup" > /dev/null 2>&1; pwd -P)"
containerd_dir="$(cd -- "${script_dir}/../.." > /dev/null 2>&1; pwd -P)"

export GOPATH="${GOPATH:-/go}"
export PATH="/usr/local/go/bin:${GOPATH}/bin:/usr/local/bin:/usr/local/sbin:${PATH}"

cd "${containerd_dir}"

echo "==> apt packages"
apt-get update -q
apt-get install -y gperf dmsetup strace xfsprogs

echo "==> libseccomp"
"${setup_dir}/install-seccomp"

echo "==> runc"
RUNC_FLAVOR=runc "${setup_dir}/install-runc"
runc --version

echo "==> CNI plugins"
"${setup_dir}/install-cni" "$(grep containernetworking/plugins go.mod | awk '{print $2}')"

echo "==> critools (critest + crictl)"
"${setup_dir}/install-critools"
critest --version
crictl --version

echo "==> runsc (gVisor)"
"${script_dir}/install-runsc"
runsc --version
containerd-shim-runsc-v1 -v

echo "==> build containerd + failpoint binaries"
make BUILDTAGS="seccomp no_btrfs no_devmapper no_zfs" binaries
make install
# failpoint shim + CNI plugin + loopback-v2 needed by several cri-integration tests
"${setup_dir}/install-failpoint-binaries"

echo "==> erofs (kernel module + userspace tools)"
modprobe erofs || true
apt-get install -y erofs-utils || true   # best-effort; tests skip gracefully if unavailable

echo "==> configure containerd for runsc (CGROUP_DRIVER=${CGROUP_DRIVER})"
CGROUP_DRIVER="${CGROUP_DRIVER}" "${script_dir}/config-containerd-runsc"

echo ""
echo "Setup complete. Run tests with:"
echo "  sudo CGROUP_DRIVER=${CGROUP_DRIVER} script/runsc/run-runsc-tests.sh"
