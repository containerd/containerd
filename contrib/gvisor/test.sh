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

set -euo pipefail

cd "$(dirname "$0")/../.."

image=containerd-gvisor-test:local

docker build --tag "$image" --file contrib/gvisor/Dockerfile .

test_log=$(mktemp)
container_id=

cleanup() {
    printf '\nTest report (exit code %s):\n' "$?"
    awk '
        !cri_done && /^--- PASS:/ { passed++ }
        !cri_done && /^--- FAIL:/ { failed++ }
        !cri_done && /^--- SKIP:/ { skipped++ }
        !cri_done && /^(PASS|FAIL)$/ {
            printf "CRI integration: %d passed | %d failed | %d skipped\n", passed, failed, skipped
            cri_done = 1
        }
        /Ran .* of .* Specs|SUCCESS!|FAIL!/ { print }
    ' "$test_log"
    rm -f "$test_log"
    [[ -z "$container_id" ]] || docker rm --force --volumes "$container_id" >/dev/null
}

trap cleanup EXIT
trap 'exit 1' INT TERM

container_id=$(docker run --detach --privileged --cgroupns=private \
    --tmpfs /run --tmpfs /run/lock --tmpfs /tmp:exec \
    --sysctl net.ipv6.conf.all.disable_ipv6=0 \
    --env "CGROUP_DRIVER=${CGROUP_DRIVER:-systemd}" \
    --env FOCUS --env FOCUS_TEST \
    "$image")

for attempt in {1..30}; do
    state=$(docker exec "$container_id" systemctl is-system-running 2>/dev/null || true)
    case "$state" in running|degraded) break ;; esac
    if [[ "$attempt" -eq 30 ]]; then
        docker logs "$container_id" >&2
        exit 1
    fi
    sleep 1
done

docker exec --interactive "$container_id" bash -s 2>&1 <<'EOF' | tee "$test_log"
set -euo pipefail

# AppArmor tests need access to the kernel policy interface.
if [[ -d /sys/module/apparmor ]]; then
    mount -t securityfs securityfs /sys/kernel/security
fi

cfg=$(mktemp /tmp/containerd-config-runsc-XXXXXX.toml)
trap 'rm -f "$cfg"' EXIT

script/runsc/write-runsc-shim-config.sh "$CGROUP_DRIVER" systrap
script/runsc/write-containerd-runsc-config.sh "$cfg" "$CGROUP_DRIVER"

CGROUP_DRIVER='' \
  CONTAINERD_CONFIG_FILE="$cfg" \
  RUNTIME=runsc RUNTIME_TYPE=io.containerd.runsc.v1 \
  make cri-integration

script/runsc/write-containerd-runsc-config.sh "$cfg" "$CGROUP_DRIVER"

GOMAXPROCS=2 GOMEMLIMIT=4GiB GINKGO_NODES=2 TEST_RUNTIME=io.containerd.runsc.v1 \
    CONTAINERD_CONFIG_FILE="$cfg" script/runsc/critest-runsc.sh "$PWD/report"
EOF
