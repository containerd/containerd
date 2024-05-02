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

set -eu -o pipefail

report_dir=$1

mkdir -p $report_dir
BDIR="$(mktemp -d -p $PWD)"

function cleanup() {
    pkill containerd || true
    echo ::group::containerd logs
    cat "$report_dir/containerd.log"
    echo ::endgroup::
    rm -rf ${BDIR}
}
trap cleanup EXIT

mkdir -p ${BDIR}/{root,state}
cat > ${BDIR}/config.toml <<EOF
version = 2
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
runtime_type = "${TEST_RUNTIME}"
[plugins."io.containerd.snapshotter.v1.overlayfs"]
# slow_chown is needed to avoid an error with kernel < 5.19:
# > "snapshotter \"overlayfs\" doesn't support idmap mounts on this host,
# > configure \`slow_chown\` to allow a slower and expensive fallback"
# https://github.com/containerd/containerd/pull/9920#issuecomment-1978901454
# This is safely ignored for kernel >= 5.19.
slow_chown = true
EOF
ls /etc/cni/net.d

/usr/local/bin/containerd \
    -a ${BDIR}/c.sock \
    --config ${BDIR}/config.toml \
    --root ${BDIR}/root \
    --state ${BDIR}/state \
    --log-level debug &> "$report_dir/containerd.log" &

# Make sure containerd is ready before calling critest.
for i in $(seq 1 10)
do
    crictl --runtime-endpoint ${BDIR}/c.sock info && break || sleep 1
done

critest --report-dir "$report_dir" --runtime-endpoint=unix:///${BDIR}/c.sock --parallel=8 "${EXTRA_CRITEST_OPTIONS:-""}"
