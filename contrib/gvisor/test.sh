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

# Keep resources available to the EXIT trap after main returns.
report_dir=
container_id=

die() {
    echo "ERROR: $*" >&2
    exit 1
}

get_go_version() {
    local version
    # install-go has a single default for its Go version.
    version=$(grep 'default:' .github/actions/install-go/action.yml | sed 's/#.*//' |
        grep -oE '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -n1) ||
        die "unable to read the Go version from .github/actions/install-go/action.yml."
    printf '%s\n' "$version"
}

check_environment() {
    local cgroup_driver=${1:-} info cgroup_version security_options

    case $cgroup_driver in
        systemd|cgroupfs) ;;
        *) die "CGROUP_DRIVER must be systemd or cgroupfs (got '$cgroup_driver')." ;;
    esac

    # '|' keeps an empty CgroupVersion from shifting the fields.
    info=$(docker info --format '{{.CgroupVersion}}|{{join .SecurityOptions " "}}') ||
        die "failed to query the Docker daemon."
    IFS='|' read -r cgroup_version security_options <<< "$info"

    [[ $cgroup_version == 2 ]] ||
        die "the tests require cgroup v2 (detected: ${cgroup_version:-unknown})."
    [[ $security_options != *name=rootless* ]] ||
        die "the tests require a rootful Docker daemon, but rootless mode is enabled."
    docker buildx version >/dev/null 2>&1 ||
        die "Docker Buildx with BuildKit is required to build the test image."
}

build_image() {
    local go_version=$1 image_id_file=$2
    docker buildx build --load --iidfile "$image_id_file" \
        --build-arg "GO_VERSION=$go_version" --file contrib/gvisor/Dockerfile . ||
        die "failed to build the gVisor test image."
}

start_container() {
    local image_id=$1 report_dir=$2 cgroup_driver=$3
    docker run --detach --privileged --cgroupns=private \
        --tmpfs /run --tmpfs /run/lock --tmpfs /tmp:exec \
        --sysctl net.ipv6.conf.all.disable_ipv6=0 \
        --env "CGROUP_DRIVER=$cgroup_driver" \
        --env FOCUS --env FOCUS_TEST \
        --mount "type=bind,src=$report_dir,dst=/report" \
        "$image_id" || die "failed to start the gVisor test container."
}

wait_for_systemd() {
    local container_id=$1 attempt state
    for attempt in {1..30}; do
        state=$(docker exec "$container_id" systemctl is-system-running 2>/dev/null || true)
        case "$state" in running|degraded) return 0 ;; esac
        if [[ "$attempt" -eq 30 ]]; then
            docker logs "$container_id" >&2 || true
            die "systemd did not become ready after 30 attempts."
        fi
        sleep 1
    done
}

print_test_report() {
    local exit_code=$1 test_log=$2
    printf '\nTest report (exit code %s):\n' "$exit_code"
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
}

cleanup() {
    local exit_code=$1 cleanup_code=0
    if [[ -f "$report_dir/test.log" ]]; then
        print_test_report "$exit_code" "$report_dir/test.log" || cleanup_code=1
    else
        cleanup_code=1
    fi
    printf 'Reports: %s\n' "$report_dir" || cleanup_code=1
    rm -f "$report_dir/image-id" || cleanup_code=1
    if [[ -n "$container_id" ]]; then
        docker rm --force --volumes "$container_id" >/dev/null || cleanup_code=1
    fi
    if [[ "$exit_code" -ne 0 ]]; then
        exit "$exit_code"
    fi
    exit "$cleanup_code"
}

run_tests() {
    local container_id=$1 test_log=$2
    docker exec --interactive "$container_id" bash -s 2>&1 <<'EOF' | tee "$test_log"
set -euo pipefail

# AppArmor tests need access to the kernel policy interface.
if [[ -d /sys/module/apparmor ]] && ! mountpoint -q /sys/kernel/security; then
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
    CONTAINERD_CONFIG_FILE="$cfg" script/runsc/critest-runsc.sh /report
EOF
}

main() {
    local cgroup_driver go_version image_id_file image_id

    cd "$(dirname "$0")/../.." || die "failed to locate the repository root."

    cgroup_driver=${CGROUP_DRIVER:-systemd}
    check_environment "$cgroup_driver"
    go_version=$(get_go_version) && [[ -n $go_version ]] ||
        die "failed to detect the Go version."

    report_dir=$(mktemp -d "${TMPDIR:-/tmp}/containerd-gvisor-report.XXXXXX") ||
        die "failed to create the report directory."
    trap 'cleanup "$?"' EXIT
    trap 'die "test run interrupted."' INT TERM

    image_id_file="$report_dir/image-id"
    touch "$report_dir/test.log" || die "failed to create the test log."

    build_image "$go_version" "$image_id_file"
    image_id=$(cat "$image_id_file") || die "failed to read the test image ID."
    container_id=$(start_container "$image_id" "$report_dir" "$cgroup_driver")
    wait_for_systemd "$container_id"
    run_tests "$container_id" "$report_dir/test.log"
}

main "$@"
