# -*- mode: ruby -*-
# vi: set ft=ruby :

#   Copyright The containerd Authors.
#
#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at

#       http://www.apache.org/licenses/LICENSE-2.0

#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.

# Vagrantfile for cgroup2
Vagrant.configure("2") do |config|
  config.vm.box = "fedora/32-cloud-base"
  config.vm.provider :virtualbox do |v|
    v.memory = 2048
    v.cpus = 2
  end
  config.vm.provider :libvirt do |v|
    v.memory = 2048
    v.cpus = 2
  end
  config.vm.provision "shell", inline: <<-SHELL
    set -eux -o pipefail
    # configuration
    GO_VERSION="1.13.12"
    RUNC_FLAVOR="crun"

    # install dnf deps
    dnf install -y gcc git libseccomp-devel lsof make

    # install Go
    curl -fsSL "https://dl.google.com/go/go${GO_VERSION}.linux-amd64.tar.gz" | tar Cxz /usr/local

    # setup env vars
    cat >> /etc/profile.d/sh.local <<EOF
PATH=/usr/local/go/bin:$PATH
GO111MODULE=off
export PATH GO111MODULE
EOF
    source /etc/profile.d/sh.local

    # enter /root/go/src/github.com/containerd/containerd
    mkdir -p /root/go/src/github.com/containerd
    ln -s /vagrant /root/go/src/github.com/containerd/containerd
    cd /root/go/src/github.com/containerd/containerd

    # install runc (or crun)
    RUNC_FLAVOR=$RUNC_FLAVOR ./script/setup/install-runc

    # install containerd
    make BUILDTAGS="no_aufs no_btrfs no_devmapper no_zfs" binaries install

    # create /integration.sh
    cat > /integration.sh <<EOF
#!/bin/bash
set -eux -o pipefail
cd /root/go/src/github.com/containerd/containerd
make integration EXTRA_TESTFLAGS=-no-criu TEST_RUNTIME=io.containerd.runc.v2 RUNC_FLAVOR=$RUNC_FLAVOR
EOF
    chmod +x /integration.sh
 SHELL
end
