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

DIR="$(dirname "${0}")"

cd "${DIR}"
go build -o checkcriu

if ! "./checkcriu"; then
	echo >&2 "ERROR: CRIU check failed"
	exit 1
fi

if [ ! -e "$(command -v crictl)" ]; then
	echo >&2 "ERROR: crictl binary not found"
	exit 1
fi

TESTDIR=$(mktemp -d -p "${PWD}")

SUCCESS=0

function cleanup() {
	rm -f ./checkcriu
	crictl ps -a || true
	pkill containerd || true
	if [ "${SUCCESS}" == "1" ]; then
		echo PASS
	else
		echo "--> containerd logs"
		sed 's/^/----> \t/' <"${TESTDIR}/containerd.log"
		echo FAIL
	fi
	umount "$(find "${TESTDIR}" -name shm -type d | head -1)" >/dev/null 2>&1 || true
	umount "$(find "${TESTDIR}" -name rootfs -type d | head -1)" >/dev/null 2>&1 || true
	rm -rf "${TESTDIR}" || true
}
trap cleanup EXIT

TESTDATA=testdata
# shellcheck disable=SC2034
export CONTAINERD_ADDRESS="$TESTDIR/c.sock"
export CONTAINER_RUNTIME_ENDPOINT="unix:///${CONTAINERD_ADDRESS}"
TEST_IMAGE=ghcr.io/containerd/alpine

function test_from_archive() {
	echo "--> Deleting all pods: "
	crictl -t 5s rmp -fa | sed 's/^/----> \t/'
	echo -n "--> Pulling base image ${TEST_IMAGE}: "
	crictl pull "${TEST_IMAGE}"
	POD_JSON=$(mktemp)
	# adapt the log directory
	jq ".log_directory=\"${TESTDIR}\"" "$TESTDATA"/sandbox_config.json >"$POD_JSON"
	echo -n "--> Start pod: "
	pod_id=$(crictl runp "$POD_JSON")
	echo "$pod_id"
	echo -n "--> Create container: "
	ctr_id=$(crictl create "$pod_id" "$TESTDATA"/container_sleep.json "$POD_JSON")
	echo "$ctr_id"
	echo -n "--> Start container: "
	crictl start "$ctr_id"
	lines_before=$(crictl logs "$ctr_id" | wc -l)
	# changes file system to see if changes are included in the checkpoint
	echo "--> Modifying container rootfs"
	crictl exec "$ctr_id" touch /root/testfile
	crictl exec "$ctr_id" rm /etc/motd
	echo -n "--> Checkpointing container: "
	crictl -t 10s checkpoint --export="$TESTDIR"/cp.tar "$ctr_id"
	echo "--> Cleanup container: "
	crictl rm -f "$ctr_id" | sed 's/^/----> \t/'
	echo "--> Cleanup pod: "
	crictl rmp -f "$pod_id" | sed 's/^/----> \t/'
	echo "--> Cleanup images: "
	crictl rmi "${TEST_IMAGE}" | sed 's/^/----> \t/'
	echo -n "--> Start pod: "
	pod_id=$(crictl runp "$POD_JSON")
	echo "$pod_id"
	# Replace original container with checkpoint image
	RESTORE_JSON=$(mktemp)
	jq ".image.image=\"$TESTDIR/cp.tar\"" "$TESTDATA"/container_sleep.json >"$RESTORE_JSON"
	echo -n "--> Create container from checkpoint: "
	# This requires a larger timeout as we just deleted the image and
	# pulling can take some time.
	ctr_id=$(crictl -t 20s create "$pod_id" "$RESTORE_JSON" "$POD_JSON")
	echo "$ctr_id"
	rm -f "$RESTORE_JSON" "$POD_JSON"
	echo -n "--> Start container from checkpoint: "
	crictl start "$ctr_id"
	sleep 1
	lines_after=$(crictl logs "$ctr_id" | wc -l)
	if [ "$lines_before" -ge "$lines_after" ]; then
		echo "number of lines after checkpointing ($lines_after) " \
			"should be larger than before checkpointing ($lines_before)"
		false
	fi
	# Cleanup
	echo "--> Cleanup images: "
	crictl rmi "${TEST_IMAGE}" | sed 's/^/----> \t/'
	echo -n "--> Verifying container rootfs: "
	crictl exec "$ctr_id" ls -la /root/testfile
	if crictl exec "$ctr_id" ls -la /etc/motd >/dev/null 2>&1; then
		echo "error: file /etc/motd should not exist but it does"
		exit 1
	fi
	echo "--> Deleting all pods: "
	crictl -t 5s rmp -fa | sed 's/^/----> \t/'
	SUCCESS=1
}

function test_from_oci() {
	echo "--> Deleting all pods: "
	crictl -t 5s rmp -fa | sed 's/^/----> \t/'
	echo -n "--> Pulling base image ${TEST_IMAGE}: "
	crictl pull "${TEST_IMAGE}"
	echo -n "--> Start pod: "
	pod_id=$(crictl runp "$TESTDATA"/sandbox_config.json)
	echo "$pod_id"
	echo -n "--> Create container: "
	ctr_id=$(crictl create "$pod_id" "$TESTDATA"/container_sleep.json "$TESTDATA"/sandbox_config.json)
	echo "$ctr_id"
	echo -n "--> Start container: "
	crictl start "$ctr_id"
	echo -n "--> Checkpointing container: "
	crictl -t 10s checkpoint --export="$TESTDIR"/cp.tar "$ctr_id"
	echo "--> Cleanup container: "
	crictl rm -f "$ctr_id" | sed 's/^/----> \t/'
	echo "--> Cleanup pod: "
	crictl rmp -f "$pod_id" | sed 's/^/----> \t/'
	echo "--> Cleanup pod: "
	crictl rmi "${TEST_IMAGE}" | sed 's/^/----> \t/'
	# Change cgroup of new sandbox
	RESTORE_POD_JSON=$(mktemp)
	jq ".linux.cgroup_parent=\"different_cgroup_789\"" "$TESTDATA"/sandbox_config.json >"$RESTORE_POD_JSON"
	echo -n "--> Start pod: "
	pod_id=$(crictl runp "$RESTORE_POD_JSON")
	echo "$pod_id"
	# Replace original container with checkpoint image
	RESTORE_JSON=$(mktemp)
	# Convert tar checkpoint archive to OCI image
	echo "--> Convert checkpoint archive $TESTDIR/cp.tar to OCI image 'checkpoint-image:latest': "
	newcontainer=$(buildah from scratch)
	echo -n "----> Add checkpoint archive to new OCI image: "
	buildah add "$newcontainer" "$TESTDIR"/cp.tar /
	echo "----> Add checkpoint annotation to new OCI image: "
	buildah config --annotation=org.criu.checkpoint.container.name=test "$newcontainer"
	echo "----> Save new OCI image: "
	buildah commit -q "$newcontainer" checkpoint-image:latest 2>&1 | sed 's/^/------> \t/'
	echo "----> Cleanup temporary images: "
	buildah rm "$newcontainer" | sed 's/^/------> \t/'
	# Export OCI image to disk
	echo "----> Export OCI image to disk: "
	podman image save -q --format oci-archive -o "$TESTDIR"/oci.tar localhost/checkpoint-image:latest | sed 's/^/------> \t/'
	echo "----> Cleanup temporary images: "
	buildah rmi localhost/checkpoint-image:latest | sed 's/^/------> \t/'
	# Remove potentially old version of the checkpoint image
	echo "----> Cleanup potential old copies: "
	../../bin/ctr -n k8s.io images rm --sync localhost/checkpoint-image:latest 2>&1 | sed 's/^/------> \t/'
	# Import image
	echo "----> Import new image: "
	../../bin/ctr -n k8s.io images import "$TESTDIR"/oci.tar 2>&1 | sed 's/^/------> \t/'
	jq ".image.image=\"localhost/checkpoint-image:latest\"" "$TESTDATA"/container_sleep.json >"$RESTORE_JSON"
	echo -n "--> Create container from checkpoint: "
	ctr_id=$(crictl -t 10s create "$pod_id" "$RESTORE_JSON" "$RESTORE_POD_JSON")
	echo "$ctr_id"
	rm -f "$RESTORE_JSON" "$RESTORE_POD_JSON"
	echo -n "--> Start container from checkpoint: "
	crictl start "$ctr_id"
	# Cleanup
	echo "--> Cleanup images: "
	../../bin/ctr -n k8s.io images rm localhost/checkpoint-image:latest | sed 's/^/----> \t/'
	echo "--> Cleanup images: "
	crictl rmi "${TEST_IMAGE}" | sed 's/^/----> \t/'
	echo "--> Deleting all pods: "
	crictl -t 5s rmp -fa | sed 's/^/----> \t/'
	SUCCESS=1
}

cat >"${TESTDIR}/config.toml" <<EOF
version = 3
[plugins."io.containerd.cri.v1.runtime".containerd]
  default_runtime_name = "test-runtime"
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.test-runtime]
runtime_type = "${TEST_RUNTIME:-io.containerd.runc.v2}"
EOF

mkdir -p "${TESTDIR}"/{root,state}

echo "--> Starting containerd: "
../../bin/containerd \
	--address "${TESTDIR}/c.sock" \
	--config "${TESTDIR}/config.toml" \
	--root "${TESTDIR}/root" \
	--state "${TESTDIR}/state" \
	--log-level trace &>"${TESTDIR}/containerd.log" &
# Make sure containerd is ready before calling critest.
retry_counter=0
max_retries=10
while true; do
	((retry_counter += 1))
	if crictl info 2>&1 | sed 's/^/----> \t/'; then
		break
	else
		sleep 1.5
	fi
	if [ "${retry_counter}" -gt "${max_retries}" ]; then
		echo "--> Failed to start containerd"
		exit 1
	fi

done

test_from_archive
SUCCESS=0
test_from_oci
