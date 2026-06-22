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

# This script is used to do extra initialization on GCI.

CONTAINERD_HOME="/home/containerd"
CONTAINERD_ENV_METADATA="containerd-env"

if [ -f "${CONTAINERD_HOME}/${CONTAINERD_ENV_METADATA}" ]; then
  source "${CONTAINERD_HOME}/${CONTAINERD_ENV_METADATA}"
fi

# CONTAINERD_COS_CGROUP_MODE can be specified as "v1" or "v2". If specified,
# cgroup configuration will be switched as appropriate.
function configure_cgroup_mode() {
  if [[ -z "${CONTAINERD_COS_CGROUP_MODE}" ]]; then
    return
  fi

  if [[ ! -r /etc/os-release ]]; then
    echo "Skipped configuring cgroup mode to ${CONTAINERD_COS_CGROUP_MODE} because /etc/os-release was not readable"
    return
  fi

  OS_ID="$(cat /etc/os-release | grep '^ID=' | sed -e 's/ID=//')"
  if [[ "${OS_ID}" != "cos" ]]; then
    echo "Skipped configuring cgroup mode to ${CONTAINERD_COS_CGROUP_MODE} because OS is not COS"
    return
  fi

  # cgroup_helper was introduced in COS M97, see if it's available first...
  if ! command -v cgroup_helper > /dev/null 2>&1; then
    echo "Skipped configuring cgroup mode to ${CONTAINERD_COS_CGROUP_MODE} because cgroup_helper tool is not available (only introduced in COS M97)"
    return
  fi

  # if cgroup mode requested was v1 but it's currently set as unified (v2),
  # switch to hybrid (v1) and reboot
  if [[ "${CONTAINERD_COS_CGROUP_MODE:-}" == "v1" ]] && cgroup_helper show | grep -q 'unified'; then
    cgroup_helper set hybrid
    echo "set cgroup config to hybrid, now rebooting..."
    reboot
  # if cgroup mode requested was v2 but it's currently set as hybrid (v1),
  # switch to unified (v2) and reboot
  elif [[ "${CONTAINERD_COS_CGROUP_MODE:-}" == "v2" ]] && cgroup_helper show | grep -q 'hybrid'; then
    cgroup_helper set unified
    echo "set cgroup config to unified, now rebooting..."
    reboot
  fi
}

configure_cgroup_mode

mount /tmp /tmp -o remount,exec,suid
#TODO(random-liu): Stop docker and remove this docker thing.
usermod -a -G docker jenkins
#TODO(random-liu): Change current node e2e to use init script,
# so that we don't need to copy this code everywhere.
mkdir -p /var/lib/kubelet
mkdir -p /home/kubernetes/containerized_mounter/rootfs
mount --bind /home/kubernetes/containerized_mounter/ /home/kubernetes/containerized_mounter/
mount -o remount, exec /home/kubernetes/containerized_mounter/
wget https://storage.googleapis.com/kubernetes-release/gci-mounter/mounter.tar -O /tmp/mounter.tar
tar xvf /tmp/mounter.tar -C /home/kubernetes/containerized_mounter/rootfs
mkdir -p /home/kubernetes/containerized_mounter/rootfs/var/lib/kubelet
mount --rbind /var/lib/kubelet /home/kubernetes/containerized_mounter/rootfs/var/lib/kubelet
mount --make-rshared /home/kubernetes/containerized_mounter/rootfs/var/lib/kubelet
mount --bind /proc /home/kubernetes/containerized_mounter/rootfs/proc
mount --bind /dev /home/kubernetes/containerized_mounter/rootfs/dev
rm /tmp/mounter.tar
