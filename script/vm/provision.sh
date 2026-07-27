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

#
# Provisions a virtual machine for running the containerd integration
# tests. Expects Fedora or an EL distribution as the guest OS.
#
# The containerd source tree has to be copied into the guest beforehand,
# and this script has to be executed as the root user inside the guest.
# See README.md in this directory for the usage.
#
set -eux -o pipefail

if [[ "$(id -u)" != "0" ]]; then
	echo "must be executed as the root user" >&2
	exit 1
fi

script_dir="$(cd -- "$(dirname -- "$0")" > /dev/null 2>&1; pwd -P)"
containerd_dir="$(cd -- "${script_dir}/../.." > /dev/null 2>&1; pwd -P)"

: "${GO_VERSION:=1.26.5}"
: "${RUNC_FLAVOR:=runc}"
: "${SELINUX:=Enforcing}"
: "${INSTALL_PACKAGES:=}"

export GOPATH="${GOPATH:-/go}"
export PATH="/usr/local/go/bin:${GOPATH}/bin:/usr/local/bin:/usr/local/sbin:${PATH}"

# Install the packages
dnf -y makecache --refresh
# shellcheck disable=SC2086
dnf -y install \
	container-selinux \
	curl \
	gcc \
	git \
	iptables \
	libseccomp-devel \
	libselinux-devel \
	lsof \
	make \
	strace \
	which \
	"kernel-modules-extra-$(uname -r)" \
	${INSTALL_PACKAGES}
modprobe xt_comment

# Install Go
GOARCH="$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')"
curl -fsSL "https://dl.google.com/go/go${GO_VERSION}.linux-${GOARCH}.tar.gz" | tar Cxz /usr/local

# The source tree is copied from the host: it may be owned by a non-root user,
# and may be labeled with the SELinux context of the directory it was copied to
git config --system --add safe.directory "${containerd_dir}"
if type -p restorecon > /dev/null; then
	restorecon -R "${containerd_dir}"
fi

# Set up $GOPATH/src/github.com/containerd/containerd
mkdir -p "${GOPATH}/src/github.com/containerd"
if [[ "${containerd_dir}" != "${GOPATH}/src/github.com/containerd/containerd" ]]; then
	ln -fnsv "${containerd_dir}" "${GOPATH}/src/github.com/containerd/containerd"
fi

cd "${containerd_dir}"

# Install runc (pass RUNC_FLAVOR=crun to install crun as runc)
RUNC_FLAVOR="${RUNC_FLAVOR}" script/setup/install-runc
type runc
runc --version
# "type -ap" may print the same file under multiple names, e.g., on
# distros where /usr/local/sbin is merged into /usr/local/bin
type -ap runc | sort -u | xargs chcon -v -t container_runtime_exec_t

# Install CNI plugins
script/setup/install-cni
CNI_BINARIES="bridge dhcp flannel host-device host-local ipvlan loopback macvlan portmap ptp tuning vlan"
# shellcheck disable=SC2086
PATH="/opt/cni/bin:${PATH}" type ${CNI_BINARIES} || true

# Install cri-tools
GOBIN=/usr/local/bin script/setup/install-critools
type crictl critest
critest --version

# Install containerd
make BUILDTAGS="seccomp selinux no_btrfs no_devmapper no_zfs" binaries install
type containerd
containerd --version
chcon -v -t container_runtime_exec_t /usr/local/bin/{containerd,containerd-shim*}

# Install gotestsum
script/setup/install-gotestsum
cp "${GOPATH}/bin/gotestsum" /usr/local/bin/

# Install failpoint binaries
script/setup/install-failpoint-binaries
type -ap containerd-shim-runc-fp-v1 | sort -u | xargs chcon -v -t container_runtime_exec_t
containerd-shim-runc-fp-v1 -v

# Configure SELinux (pass SELINUX=Disabled or SELINUX=Permissive to weaken it)
# and establish /etc/containerd/config.toml
SELINUX="${SELINUX}" script/setup/config-selinux
script/setup/config-containerd
