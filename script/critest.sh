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
    # Preserve the original exit code
    local exit_code=$?

    # Only kill containerd if coverage didn't already handle it
    if [ -z "${GOCOVERDIR:-}" ]; then
        pkill containerd || true
    fi

    echo ::group::containerd logs
    cat "$report_dir/containerd.log" || true
    echo ::endgroup::
    rm -rf ${BDIR} || true

    # Exit with the preserved exit code
    exit $exit_code
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

if [ ! -z "$CGROUP_DRIVER" ] && [ "$CGROUP_DRIVER" = "systemd" ];then
  cat >> ${BDIR}/config.toml <<EOF
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
   SystemdCgroup = true
EOF
fi

GINKGO_SKIP_TEST=()
if [ -n "${SKIP_TEST:-}" ]; then
  GINKGO_SKIP_TEST+=("--ginkgo.skip" "$SKIP_TEST")
fi

GINKGO_FOCUS_TEST=()
if [ -n "${FOCUS_TEST:-}" ]; then
  GINKGO_FOCUS_TEST+=("--ginkgo.focus" "$FOCUS_TEST")
fi

ls /etc/cni/net.d

# Use containerd-coverage binary if GOCOVERDIR is set, otherwise use regular containerd
if [ -n "${GOCOVERDIR:-}" ]; then
    echo "Running containerd with coverage enabled (GOCOVERDIR=$GOCOVERDIR)"
    mkdir -p "$GOCOVERDIR"
    CONTAINERD_BIN=/usr/local/bin/containerd-coverage
else
    CONTAINERD_BIN=/usr/local/bin/containerd
fi

$CONTAINERD_BIN \
    -a ${BDIR}/c.sock \
    --config ${BDIR}/config.toml \
    --root ${BDIR}/root \
    --state ${BDIR}/state \
    --log-level debug &> "$report_dir/containerd.log" &
CONTAINERD_PID=$!

# Make sure containerd is ready before calling critest.
for i in $(seq 1 10)
do
    crictl --runtime-endpoint ${BDIR}/c.sock info && break || sleep 1
done

# Run critest but do not let set -e stop execution so we can always perform
# teardown/coverage flush. Capture the test exit code and exit with it.
set +e
critest --report-dir "$report_dir" --runtime-endpoint=unix:///${BDIR}/c.sock --parallel=8 "${GINKGO_SKIP_TEST[@]}" "${GINKGO_FOCUS_TEST[@]}" "${EXTRA_CRITEST_OPTIONS:-""}"
_CRITEST_RC=$?
set -e

# If coverage is enabled, signal containerd to flush coverage and wait for it to exit
if [ -n "${GOCOVERDIR:-}" ]; then
    echo "Flushing coverage data..."
    sudo kill -TERM "$CONTAINERD_PID" || true
    # Wait for containerd to exit gracefully
    sleep 3
fi

# Exit with the captured critest return code so the workflow status reflects tests
exit "${_CRITEST_RC}"
