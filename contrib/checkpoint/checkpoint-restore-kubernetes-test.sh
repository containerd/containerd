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

if [ ! -e "$(command -v kubectl)" ]; then
	echo >&2 "ERROR: kubectl binary not found"
	exit 1
fi

if [ ! -e "$(command -v ctr)" ]; then
	echo >&2 "ERROR: ctr binary not found"
	exit 1
fi

TESTDIR=$(mktemp -d)
OUTPUT=$(mktemp)
SUCCESS=0

function cleanup() {
	# shellcheck disable=SC2317
	rm -f ./checkcriu
	# shellcheck disable=SC2317
	rm -rf "${TESTDIR}" "${OUTPUT}"
	# shellcheck disable=SC2317
	if [ "${SUCCESS}" == "1" ]; then
		# shellcheck disable=SC2317
		echo PASS
	else
		# shellcheck disable=SC2317
		echo FAIL
	fi
}
trap cleanup EXIT

TESTDATA=testdata
export CONTAINER_RUNTIME_ENDPOINT="unix:///run/containerd/containerd.sock"
export KUBECONFIG=/var/run/kubernetes/admin.kubeconfig

echo -n "--> Cleanup test pod/containers from previous run: "
kubectl delete pod sleeper --grace-period=1 || true
echo -n "--> Create new test pod/containers: "
kubectl apply -f $TESTDATA/sleep.yaml

echo "--> Wait until test pod/containers are ready: "
while [ "$(kubectl get pod sleeper -o jsonpath="{.status.containerStatuses[0].started}")" == "false" ]; do
	echo "----> Waiting for pod/container sleeper/sleep to get ready"
	sleep 0.5
done

echo "--> Do curl request to the test container: "
curl -s "$(kubectl get pod sleeper --template '{{.status.podIP}}'):8088" | tee "${OUTPUT}" | sed 's/^/----> \t/'
COUNTER_BEFORE=$(cut -d\  -f2 <"${OUTPUT}")
echo "--> Check logs of test container: "
kubectl logs sleeper -c sleep | tee "${OUTPUT}" | sed 's/^/----> \t/'
LINES_BEFORE=$(wc -l <"${OUTPUT}")

if [ ! -e /var/run/kubernetes/client-admin.crt ]; then
	echo "Missing /var/run/kubernetes/client-admin.crt. Exiting"
	exit 1
fi

if [ ! -e /var/run/kubernetes/client-admin.key ]; then
	echo "Missing /var/run/kubernetes/client-admin.key. Exiting"
	exit 1
fi

echo -n "--> Creating checkpoint: "
CP=$(curl -s --insecure --cert /var/run/kubernetes/client-admin.crt --key /var/run/kubernetes/client-admin.key -X POST "https://localhost:10250/checkpoint/default/sleeper/sleep" | jq -r ".items[0]")
echo "$CP"

echo -n "--> Cleanup test pod/containers: "
kubectl delete pod sleeper --grace-period=1

OCI="localhost/checkpoint-image:latest"

echo "--> Converting checkpoint archive $CP to OCI images $OCI"

newcontainer=$(buildah from scratch)
echo -n "----> Add checkpoint archive to new OCI image: "
buildah add "$newcontainer" "$CP" /
echo "----> Add checkpoint annotation to new OCI image: "
buildah config --annotation=org.criu.checkpoint.container.name=test "$newcontainer"
echo "----> Save new OCI image: "
buildah commit -q "$newcontainer" "$OCI" 2>&1 | sed 's/^/------> \t/'
echo "----> Cleanup temporary images: "
buildah rm "$newcontainer" | sed 's/^/------> \t/'
# Export OCI image to disk
echo "----> Export OCI image to disk: "
podman image save -q --format oci-archive -o "$TESTDIR"/oci.tar "$OCI" | sed 's/^/------> \t/'
echo "----> Cleanup temporary images: "
buildah rmi "$OCI" | sed 's/^/------> \t/'
# Remove potentially old version of the checkpoint image
echo "----> Cleanup potential old copies: "
ctr -n k8s.io images rm --sync localhost/checkpoint-image:latest 2>&1 | sed 's/^/------> \t/'
# Import image
echo "----> Import checkpoint image: "
ctr -n k8s.io images import "$TESTDIR"/oci.tar 2>&1 | sed 's/^/------> \t/'

echo -n "--> Create pod/containers from checkpoint: "
kubectl apply -f $TESTDATA/sleep-restore.yaml

echo "--> Wait until test pod/containers are ready: "
while [ "$(kubectl get pod sleeper -o jsonpath="{.status.containerStatuses[0].started}")" == "false" ]; do
	echo "----> Waiting for pod/container sleeper/sleep to get ready"
	sleep 0.5
done

echo "--> Do curl request to the test container: "
curl -s "$(kubectl get pod sleeper --template '{{.status.podIP}}'):8088" | tee "${OUTPUT}" | sed 's/^/----> \t/'
COUNTER_AFTER=$(cut -d\  -f2 <"${OUTPUT}")
echo "--> Check logs of test container: "
kubectl logs sleeper -c sleep | tee "${OUTPUT}" | sed 's/^/----> \t/'
LINES_AFTER=$(wc -l <"${OUTPUT}")

if [ "$LINES_BEFORE" -ge "$LINES_AFTER" ]; then
	echo "number of lines after checkpointing ($LINES_AFTER) " \
		"should be larger than before checkpointing ($LINES_BEFORE)"
	false
fi

if [ "$COUNTER_BEFORE" -ge "$COUNTER_AFTER" ]; then
	echo "number of lines after checkpointing ($COUNTER_AFTER) " \
		"should be larger than before checkpointing ($COUNTER_BEFORE)"
	false
fi

# Let's see if container restart also works correctly
CONTAINER_ID=$(kubectl get pod sleeper -o jsonpath="{.status.containerStatuses[0].containerID}" | cut -d '/' -f 3)
CONTAINER_PID=$(crictl inspect "${CONTAINER_ID}" | jq .info.pid)

echo "--> Kill container $CONTAINER_ID by killing PID $CONTAINER_PID"
# Kill PID in containner
kill -9 "${CONTAINER_PID}"

# wait for the replacement container to come up
echo "--> Wait until replacement pod/containers is started: "
while [ "$(kubectl get pod sleeper -o jsonpath="{.status.containerStatuses[0].restartCount}")" != "1" ]; do
	echo "----> Waiting for pod/container sleeper/sleep to get ready"
	sleep 1
done

echo "--> Do curl request to the test container: "
curl -s "$(kubectl get pod sleeper --template '{{.status.podIP}}'):8088" | tee "${OUTPUT}" | sed 's/^/----> \t/'
COUNTER_AFTER=$(cut -d\  -f2 <"${OUTPUT}")
echo "--> Check logs of test container: "
kubectl logs sleeper -c sleep | tee "${OUTPUT}" | sed 's/^/----> \t/'
LINES_AFTER=$(wc -l <"${OUTPUT}")

if [ "$LINES_BEFORE" -ge "$LINES_AFTER" ]; then
	echo "number of lines after checkpointing ($LINES_AFTER) " \
		"should be larger than before checkpointing ($LINES_BEFORE)"
	false
fi

if [ "$COUNTER_BEFORE" -ge "$COUNTER_AFTER" ]; then
	echo "number of lines after checkpointing ($COUNTER_AFTER) " \
		"should be larger than before checkpointing ($COUNTER_BEFORE)"
	false
fi

# Let's see if container restart also works correctly a second time

CONTAINER_ID=$(kubectl get pod sleeper -o jsonpath="{.status.containerStatuses[0].containerID}" | cut -d '/' -f 3)
CONTAINER_PID=$(crictl inspect "${CONTAINER_ID}" | jq .info.pid)

# Kill PID in containner
echo "--> Kill container $CONTAINER_ID by killing PID $CONTAINER_PID"
kill -9 "${CONTAINER_PID}"

# wait for the replacement container to come up
echo "--> Wait until replacement pod/containers is started: "
while [ "$(kubectl get pod sleeper -o jsonpath="{.status.containerStatuses[0].restartCount}")" != "2" ]; do
	echo "----> Waiting for pod/container sleeper/sleep to get ready"
	sleep 1
done

echo "--> Do curl request to the test container: "
curl -s "$(kubectl get pod sleeper --template '{{.status.podIP}}'):8088" | tee "${OUTPUT}" | sed 's/^/----> \t/'
COUNTER_AFTER=$(cut -d\  -f2 <"${OUTPUT}")
echo "--> Check logs of test container: "
kubectl logs sleeper -c sleep | tee "${OUTPUT}" | sed 's/^/----> \t/'
LINES_AFTER=$(wc -l <"${OUTPUT}")

if [ "$LINES_BEFORE" -ge "$LINES_AFTER" ]; then
	echo "number of lines after checkpointing ($LINES_AFTER) " \
		"should be larger than before checkpointing ($LINES_BEFORE)"
	false
fi

if [ "$COUNTER_BEFORE" -ge "$COUNTER_AFTER" ]; then
	echo "number of lines after checkpointing ($COUNTER_AFTER) " \
		"should be larger than before checkpointing ($COUNTER_BEFORE)"
	false
fi

echo -n "--> Creating checkpoint from restored container: "
CP=$(curl -s --insecure --cert /var/run/kubernetes/client-admin.crt --key /var/run/kubernetes/client-admin.key -X POST "https://localhost:10250/checkpoint/default/sleeper/sleep" | jq -r ".items[0]")
echo "$CP"

SUCCESS=1

exit 0
