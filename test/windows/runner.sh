#!/bin/bash

# Copyright The containerd Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

GCE_PROJECT="${GCE_PROJECT:-"cri-containerd-node-e2e"}"
GCE_IMAGE="${GCE_IMAGE:-"windows-server-1809-dc-core-for-containers-v20190827"}"
GCE_IMAGE_PROJECT="${GCE_IMAGE_PROJECT:-"windows-cloud"}"
ZONE="${ZONE:-"us-west1-b"}"
ARTIFACTS="${ARTIFACTS:-"/tmp/test-cri-windows/_artifacts"}"
CLEANUP="${CLEANUP:-"true"}"

root="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"/../..
script_path="${root}/test/windows"
node_name="windows-cri-$(cat /proc/sys/kernel/random/uuid)"

# log logs the test logs.
function log() {
  echo "$(date) $1"
}

function cleanup() {
  if [[ "$CLEANUP" == "true" ]]; then
    log "Delete the test instance"
    gcloud compute instances delete -q "${node_name}"
  fi
}

function retry() {
  local -r MAX_ATTEMPTS=$1
  local attempts=1
  shift
  until "$@" || (( attempts == MAX_ATTEMPTS ))
  do
    log "$* failed, retry in 1 second..."
    (( attempts++ ))
    sleep 1
  done
}

gcloud config set compute/zone "${ZONE}"
gcloud config set project "${GCE_PROJECT}"

log "Create the test instance"
gcloud compute instances create "${node_name}" --machine-type=n1-standard-2 \
  --image="${GCE_IMAGE}" --image-project="${GCE_IMAGE_PROJECT}" \
  --metadata-from-file=windows-startup-script-ps1="${script_path}/setup-ssh.ps1"
trap cleanup EXIT

log "Wait for ssh to be ready"
retry 180 gcloud compute ssh "${node_name}" --command="echo ssh ready"

log "Setup test environment in the test instance"
retry 5 gcloud compute scp "${script_path}/setup-vm.ps1" "${node_name}":"C:/setup-vm.ps1"
gcloud compute ssh "${node_name}" --command="powershell /c C:/setup-vm.ps1"

log "Reboot the test instance to refresh environment variables"
gcloud compute ssh "${node_name}" --command="powershell /c Restart-Computer"

log "Wait for ssh to be ready"
retry 180 gcloud compute ssh "${node_name}" --command="echo ssh ready"

log "Run test on the test instance"
cri_tar="/tmp/cri.tar.gz"
tar -zcf "${cri_tar}" -C "${root}" . --owner=0 --group=0
retry 5 gcloud compute scp "${script_path}/test.sh" "${node_name}":"C:/test.sh"
retry 5 gcloud compute scp "${cri_tar}" "${node_name}":"C:/cri.tar.gz"
rm "${cri_tar}"
# git-bash doesn't return test exit code, the command should
# succeed. We'll collect test exit code from _artifacts/.
gcloud compute ssh "${node_name}" --command='powershell /c "Start-Process -FilePath \"C:\Program Files\Git\git-bash.exe\" -ArgumentList \"-elc\",\"`\"/c/test.sh &> /c/test.log`\"\" -Wait"'

log "Collect test logs"
mkdir -p "${ARTIFACTS}"
retry 5 gcloud compute scp "${node_name}":"C:/test.log" "${ARTIFACTS}"
retry 5 gcloud compute scp --recurse "${node_name}":"C:/_artifacts/*" "${ARTIFACTS}"

log "Test output:"
cat "${ARTIFACTS}/test.log"

exit_code="$(cat "${ARTIFACTS}/exitcode")"
exit "${exit_code}"
