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

function traverse_path() {
    local path=$1
    cd "$path"
    sudo chmod go+rx "$PWD"

    while [ $PWD != "/" ]; do
	sudo chmod go+x "$PWD/../"
	cd ..
    done
}

BDIR="$(mktemp -d -p $PWD)"
# runc needs to traverse (+x) the directories in the path to the rootfs. This is important when we
# create a user namespace, as the final stage of the runc initialization is not as root on the host.
# While containerd creates the directories with the right permissions, the right group (so only the
# hostGID has access, etc.), those directories live below $BDIR. So, to make sure runc can traverse
# the directories, let's fix the dirs from $BDIR up, as the ones below are managed by containerd
# that does the right thing.
traverse_path "$BDIR"

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
