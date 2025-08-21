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

report_dir=${1:-"./reports"}
mkdir -p "$report_dir"

function traverse_path() {
    local path=$1
    cd "$path"
    sudo chmod go+rx "$PWD"

    while [ $PWD != "/" ]; do
        sudo chmod go+x "$PWD/../"
        cd ..
    done
}

# Temporary containerd root
BDIR="$(mktemp -d -p $PWD)"
traverse_path "$BDIR"
mkdir -p "$BDIR"/{root,state}

# Coverage directory
export GOCOVERDIR="$report_dir/cover"
mkdir -p "$GOCOVERDIR"

cat > "$BDIR/config.toml" <<EOF
version = 2
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
  runtime_type = "${TEST_RUNTIME:-io.containerd.runc.v2}"
[plugins."io.containerd.snapshotter.v1.overlayfs"]
  slow_chown = true
EOF

if [[ "${CGROUP_DRIVER:-}" == "systemd" ]]; then
  cat >> "$BDIR/config.toml" <<EOF
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
  SystemdCgroup = true
EOF
fi

GINKGO_SKIP_TEST=()
if [ -n "${SKIP_TEST:-}" ]; then
  GINKGO_SKIP_TEST+=("--ginkgo.skip" "$SKIP_TEST")
fi

cleanup() {
    echo "Stopping containerd-coverage..."
    sudo pkill -f -INT containerd-coverage || true
    sleep 2
    rm -rf "$BDIR"
}
trap cleanup EXIT

/usr/local/bin/containerd-coverage \
    -a "$BDIR/c.sock" \
    --config "$BDIR/config.toml" \
    --root "$BDIR/root" \
    --state "$BDIR/state" \
    --log-level debug &> "$report_dir/containerd.log" &

# Wait for containerd
for i in $(seq 1 10); do
    crictl --runtime-endpoint unix://"$BDIR/c.sock" info && break || sleep 1
done

critest \
    --report-dir "$report_dir" \
    --runtime-endpoint unix://"$BDIR/c.sock" \
    --parallel=8 \
    "${GINKGO_SKIP_TEST[@]}" \
    "${EXTRA_CRITEST_OPTIONS:-}"

# Give containerd time to flush coverage
sleep 3

# Merge coverage files into a single .out file
mkdir -p "$report_dir/cover/merged"
if compgen -G "$GOCOVERDIR/covmeta.*" > /dev/null; then
    go tool covdata merge -i "$GOCOVERDIR" -o "$report_dir/cover/merged"
    go tool covdata textfmt -i "$report_dir/cover/merged" -o "$report_dir/cover/containerd.txt"
    echo "Merged coverage .out file generated at $report_dir/cover/containerd.txt"
else
    echo "No coverage files found. Make sure containerd-coverage was used."
fi
