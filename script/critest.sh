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

if [ ! -z "$CGROUP_DRIVER" ] && [ "$CGROUP_DRIVER" = "systemd" ];then
  cat >> ${BDIR}/config.toml <<EOF
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
   SystemdCgroup = true
EOF
fi

GINKGO_SKIP_TEST=()
if [ -n "${SKIP_TEST:-}" ]; then
  GINKGO_SKIP_TEST+=("--ginkgo.skip" "$SKIP_TEST")

  # With the systemd cgroup driver, the container runtime uses a scope unit to
  # manage the cgroup path. According to the scope unit documentation:
  #
  #   Unlike service units, scope units have no “main” process: all processes in
  #   the scope are equivalent. The lifecycle of a scope unit is therefore not
  #   bound to a specific process, but to the existence of at least one process in
  #   the scope. As a result, individual process exit statuses are not relevant to
  #   the scope unit’s failure state.
  #
  # We cannot rely on CollectMode=inactive-or-failed to preserve the cgroup path.
  # So there is a race condition between containerd and systemd garbage collection.
  # If systemd GC removes the scope unit’s cgroup before containerd reads it,
  # containerd loses the opportunity to inspect the cgroup and determine the OOM status.
  #
  # So we disable the OOMKilled testcase.
  #
  # FIXME(fuweid):
  #
  # In theory, this could be mitigated by inspecting the unit logs (e.g.
  # `journalctl -u XXX.scope`) and searching for the "OOMKilled" keyword.
  # However, this approach depends on journalctl and systemd logging behavior,
  # so it should be avoided.
  #
  # Example journal output:
  #
  #   Dec 22 01:24:58 devbox systemd[1]: Started /usr/bin/bash -c dd if=/dev/zero of=/dev/null bs=20M.
  #   Dec 22 01:24:58 devbox systemd[1]: XXX.service: A process of this unit has been killed by the OOM killer.
  #   Dec 22 01:24:58 devbox systemd[1]: XXX.service: Main process exited, code=killed, status=9/KILL
  #   Dec 22 01:24:58 devbox systemd[1]: XXX.service: Failed with result 'oom-kill'.
  #
  # Ref: https://www.freedesktop.org/software/systemd/man/latest/systemd.scope.html
  if [ ! -z "$CGROUP_DRIVER" ] && [ "$CGROUP_DRIVER" = "systemd" ];then
    GINKGO_SKIP_TEST+=("--ginkgo.skip" "should terminate with exitCode 137 and reason OOMKilled")
  fi
fi

GINKGO_FOCUS_TEST=()
if [ -n "${FOCUS_TEST:-}" ]; then
  GINKGO_FOCUS_TEST+=("--ginkgo.focus" "$FOCUS_TEST")
fi

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

critest --report-dir "$report_dir" --runtime-endpoint=unix:///${BDIR}/c.sock --parallel=8 "${GINKGO_SKIP_TEST[@]}" "${GINKGO_FOCUS_TEST[@]}" "${EXTRA_CRITEST_OPTIONS:-""}"
