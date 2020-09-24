#!/bin/bash

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
tempdir="$(mktemp -d)"

# log logs the test logs.
function log() {
  echo "$(date) $1"
}

function cleanup() {
  rm -rf "$tempdir"
  if [[ "$CLEANUP" == "true" ]]; then
    log "Delete the test instance"
    gcloud compute instances delete -q "${node_name}"
  fi
}

function retry_anyway() {
  retry_on_error 36 5 "" "$@"
}

function retry_on_permission_error() {
  retry_on_error 36 5 "Permission denied" "$@"
}

function retry_on_error() {
  local -r MAX_ATTEMPTS="$1"
  local -r SLEEP_PERIOD="$2"
  local -r ON_ERROR="$3"
  shift 3
  local attempts=1
  local -r stderr="$(mktemp -p "$tempdir")"
  until "$@" 2>"$stderr"; do
    cat "$stderr"
    (( attempts++ ))
    if [[ -n "$ON_ERROR" ]] && (! grep "$ON_ERROR" "$stderr" > /dev/null); then
      log "$* failed with unexpected error!"
      exit 1
    elif (( attempts > MAX_ATTEMPTS )); then
      log "$* failed, $MAX_ATTEMPTS retry exceeded!"
      exit 1
    else
      log "$* failed, retry in ${SLEEP_PERIOD} second..."
    fi
    sleep "${SLEEP_PERIOD}"
  done
  rm "$stderr"
}

gcloud config set compute/zone "${ZONE}"
gcloud config set project "${GCE_PROJECT}"

log "Create the test instance"
gcloud compute instances create "${node_name}" --machine-type=n1-standard-2 \
  --image="${GCE_IMAGE}" --image-project="${GCE_IMAGE_PROJECT}" \
  --metadata-from-file=windows-startup-script-ps1="${script_path}/setup-ssh.ps1"
trap cleanup EXIT

ssh=(gcloud compute ssh --ssh-flag="-ServerAliveInterval=30")
scp=(gcloud compute scp)
log "Wait for ssh to be ready"
retry_anyway "${ssh[@]}" "${node_name}" --command="echo ssh ready"

log "Setup test environment in the test instance"
retry_on_permission_error "${scp[@]}" "${script_path}/setup-vm.ps1" "${node_name}":"C:/setup-vm.ps1"
retry_on_permission_error "${ssh[@]}" "${node_name}" --command="powershell /c C:/setup-vm.ps1"

log "Reboot the test instance to refresh environment variables"
retry_on_permission_error "${ssh[@]}" "${node_name}" --command="powershell /c Restart-Computer"

log "Wait for ssh to be ready"
retry_anyway "${ssh[@]}" "${node_name}" --command="echo ssh ready"

log "Run test on the test instance"
cri_tar="/tmp/cri.tar.gz"
tar -zcf "${cri_tar}" -C "${root}" . --owner=0 --group=0
retry_on_permission_error "${scp[@]}" "${script_path}/test.sh" "${node_name}":"C:/test.sh"
retry_on_permission_error "${scp[@]}" "${cri_tar}" "${node_name}":"C:/cri.tar.gz"
rm "${cri_tar}"
# git-bash doesn't return test exit code, the command should
# succeed. We'll collect test exit code from _artifacts/.
retry_on_permission_error "${ssh[@]}" "${node_name}" --command='powershell /c "Start-Process -FilePath \"C:\Program Files\Git\git-bash.exe\" -ArgumentList \"-elc\",\"`\"/c/test.sh &> /c/test.log`\"\" -Wait"'

log "Collect test logs"
mkdir -p "${ARTIFACTS}"
retry_on_permission_error "${scp[@]}" "${node_name}":"C:/test.log" "${ARTIFACTS}"
retry_on_permission_error "${scp[@]}" --recurse "${node_name}":"C:/_artifacts/*" "${ARTIFACTS}"

log "Test output:"

# Make sure stdout is not in O_NONBLOCK mode.
# See https://github.com/kubernetes/test-infra/issues/14938 for more details.
python -c 'import os,sys,fcntl; flags = fcntl.fcntl(sys.stdout, fcntl.F_GETFL); print(flags&os.O_NONBLOCK);'
python -c 'import os,sys,fcntl; flags = fcntl.fcntl(sys.stdout, fcntl.F_GETFL); fcntl.fcntl(sys.stdout, fcntl.F_SETFL, flags&~os.O_NONBLOCK);'

cat "${ARTIFACTS}/test.log"

exit_code="$(cat "${ARTIFACTS}/exitcode")"
exit "${exit_code}"
