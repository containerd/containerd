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

ARG GO_VERSION=latest
FROM golang:${GO_VERSION}
ENV DEBIAN_FRONTEND=noninteractive
ENV CGO_ENABLED=1

RUN apt-get update && apt-get install -y --no-install-recommends \
    libbtrfs-dev pkg-config bzip2 \
    systemd systemd-sysv dbus sudo apparmor iptables iproute2 lsof \
    gperf dmsetup strace xfsprogs erofs-utils \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /go/src/github.com/containerd/containerd
COPY go.mod ./
COPY script/setup/ script/setup/
COPY script/runsc/install-runsc script/runsc/runsc-version script/runsc/
RUN script/setup/install-seccomp \
    && script/setup/install-runc \
    && script/setup/install-cni "$(awk '/github.com\/containernetworking\/plugins / {print $2}' go.mod)" \
    && script/setup/install-critools \
    && script/runsc/install-runsc

COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    script/setup/install-failpoint-binaries \
    && make binaries GO_BUILD_FLAGS="-mod=vendor" \
    && make install

# Keep nested overlayfs mounts off the image's overlayfs, as in Dockerfile.test.
VOLUME ["/var/lib", "/go/src/github.com/containerd/containerd"]
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
