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

# set up cgroupv2 based on flag CONTAINERD_CGROUPV2 in containerd-env
CONTAINERD_HOME="/home/containerd"
CONTAINERD_ENV_METADATA="containerd-env"

if [ -f "${CONTAINERD_HOME}/${CONTAINERD_ENV_METADATA}" ]; then
  source "${CONTAINERD_HOME}/${CONTAINERD_ENV_METADATA}"
fi

if [ "${CONTAINERD_CGROUPV2:-"false"}"  == "true" ]; then
  # check cos image
  if uname -a | grep -q cos; then
    if ! grep -q 'systemd.unified_cgroup_hierarchy=true' /proc/cmdline; then
      echo "Setting up cgroupv2"

      mount_path="/tmp/esp"
      mkdir -p "${mount_path}"
      esp_partition="/dev/sda12"
      mount "${esp_partition}" "${mount_path}"
      sed -i 's/systemd.unified_cgroup_hierarchy=false/systemd.unified_cgroup_hierarchy=true/g' "${mount_path}/efi/boot/grub.cfg"
      umount "${mount_path}"
      rmdir "${mount_path}"

      echo "Reconfigured grub; rebooting..."
      reboot
    fi
  fi
fi

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
