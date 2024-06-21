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

function cleanup() {
	rm -f ./checkcriu
	rm -rf "${TESTDIR}"
}
trap cleanup EXIT

TESTDATA=testdata
# shellcheck disable=SC2034
CONTAINER_RUNTIME_ENDPOINT="unix:///run/containerd/containerd.sock"

function test_from_archive() {
	crictl -t 5s rmp -fa
	crictl pull quay.io/crio/fedora-crio-ci:latest
	pod_id=$(crictl runp "$TESTDATA"/sandbox_config.json)
	ctr_id=$(crictl create "$pod_id" "$TESTDATA"/container_sleep.json "$TESTDATA"/sandbox_config.json)
	crictl start "$ctr_id"
	crictl -t 10s checkpoint --export="$TESTDIR"/cp.tar "$ctr_id"
	crictl rm -f "$ctr_id"
	crictl rmp -f "$pod_id"
	crictl rmi quay.io/crio/fedora-crio-ci:latest
	crictl images
	pod_id=$(crictl runp "$TESTDATA"/sandbox_config.json)
	# Replace original container with checkpoint image
	RESTORE_JSON=$(mktemp)
	jq ".image.image=\"$TESTDIR/cp.tar\"" "$TESTDATA"/container_sleep.json >"$RESTORE_JSON"
	ctr_id=$(crictl create "$pod_id" "$RESTORE_JSON" "$TESTDATA"/sandbox_config.json)
	rm -f "$RESTORE_JSON"
	crictl start "$ctr_id"
}

function test_from_oci() {
	crictl -t 5s rmp -fa
	crictl pull quay.io/crio/fedora-crio-ci:latest
	pod_id=$(crictl runp "$TESTDATA"/sandbox_config.json)
	ctr_id=$(crictl create "$pod_id" "$TESTDATA"/container_sleep.json "$TESTDATA"/sandbox_config.json)
	crictl start "$ctr_id"
	crictl -t 10s checkpoint --export="$TESTDIR"/cp.tar "$ctr_id"
	crictl rm -f "$ctr_id"
	crictl rmp -f "$pod_id"
	crictl rmi quay.io/crio/fedora-crio-ci:latest
	crictl images
	pod_id=$(crictl runp "$TESTDATA"/sandbox_config.json)
	# Replace original container with checkpoint image
	RESTORE_JSON=$(mktemp)
	# Convert tar checkpoint archive to OCI image
	newcontainer=$(buildah from scratch)
	buildah add "$newcontainer" "$TESTDIR"/cp.tar /
	buildah config --annotation=org.criu.checkpoint.container.name=test "$newcontainer"
	buildah commit "$newcontainer" checkpoint-image:latest
	buildah rm "$newcontainer"
	# Export OCI image to disk
	podman image save --format oci-archive -o "$TESTDIR"/oci.tar localhost/checkpoint-image:latest
	# Remove potentially old version of the checkpoint image
	../../bin/ctr -n k8s.io images rm localhost/checkpoint-image:latest
	# Import image
	../../bin/ctr -n k8s.io images import "$TESTDIR"/oci.tar
	jq ".image.image=\"localhost/checkpoint-image:latest\"" "$TESTDATA"/container_sleep.json >"$RESTORE_JSON"
	ctr_id=$(crictl create "$pod_id" "$RESTORE_JSON" "$TESTDATA"/sandbox_config.json)
	rm -f "$RESTORE_JSON"
	crictl start "$ctr_id"
}

test_from_archive
test_from_oci
