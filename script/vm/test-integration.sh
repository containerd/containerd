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
# Runs the containerd integration tests inside a virtual machine
# provisioned with provision.sh.
#
set -eux -o pipefail

if [[ "$(id -u)" != "0" ]]; then
	echo "must be executed as the root user" >&2
	exit 1
fi

script_dir="$(cd -- "$(dirname -- "$0")" > /dev/null 2>&1; pwd -P)"
containerd_dir="$(cd -- "${script_dir}/../.." > /dev/null 2>&1; pwd -P)"

: "${RUNC_FLAVOR:=runc}"
export GOTEST="${GOTEST:-go test}"

export GOPATH="${GOPATH:-/go}"
export PATH="/usr/local/go/bin:${GOPATH}/bin:/usr/local/bin:/usr/local/sbin:${PATH}"

rm -rf /var/lib/containerd-test /run/containerd-test
cd "${containerd_dir}"
go test -v -count=1 -race ./core/metrics/cgroups
make integration EXTRA_TESTFLAGS="-timeout 15m -no-criu -test.v" TEST_RUNTIME=io.containerd.runc.v2 RUNC_FLAVOR="${RUNC_FLAVOR}"
