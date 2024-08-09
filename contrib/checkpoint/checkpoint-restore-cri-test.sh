#!/usr/bin/env bash

set -eu -o pipefail

DIR=$(dirname "${0}")

cd "${DIR}"
go build -o checkcriu

if ! "./checkcriu"; then
	echo "ERROR: CRIU check failed"
	exit 1
fi

if [ ! -e "$(command -v crictl)" ]; then
	echo "ERROR: crictl binary not found"
	exit 1
fi

TESTDIR=$(mktemp -d)

SUCCESS=0

function cleanup() {
	rm -f ./checkcriu
	rm -rf "${TESTDIR}"
	if [ "${SUCCESS}" == "1" ]; then
		echo PASS
	else
		echo FAIL
	fi
}
trap cleanup EXIT

TESTDATA=testdata
# shellcheck disable=SC2034
CONTAINER_RUNTIME_ENDPOINT="unix:///run/containerd/containerd.sock"
TEST_IMAGE=quay.io/adrianreber/counter:latest

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
	crictl exec "$ctr_id" touch /home/counter/testfile
	crictl exec "$ctr_id" rm /home/counter/.bash_logout
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
	crictl exec "$ctr_id" ls -la /home/counter/testfile
	if crictl exec "$ctr_id" ls -la /home/counter/.bash_logout >/dev/null 2>&1; then
		echo "error: file /etc/exports should not exist but it does"
		exit 1
	fi
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
	SUCCESS=1
}

test_from_archive
SUCCESS=0
test_from_oci
