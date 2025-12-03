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

ARG UBUNTU_VERSION=22.04
ARG BASE_IMAGE=ubuntu:${UBUNTU_VERSION}
ARG GO_VERSION=1.24.11
ARG GO_IMAGE=golang:${GO_VERSION}
FROM --platform=$BUILDPLATFORM $GO_IMAGE AS go
FROM --platform=$BUILDPLATFORM tonistiigi/xx:1.6.1@sha256:923441d7c25f1e2eb5789f82d987693c47b8ed987c4ab3b075d6ed2b5d6779a3 AS xx

FROM --platform=$BUILDPLATFORM ${BASE_IMAGE} AS base
COPY --from=xx / /
SHELL ["/bin/bash", "-xec"]
ENV DEBIAN_FRONTEND=noninteractive
RUN	apt-get update && \
	apt-get install -y dpkg-dev git make pkg-config
ARG TARGETPLATFORM
# gcc is needed for github.com/containerd/btrfs/v2
RUN xx-apt-get install -y gcc
ENV PATH=/usr/local/go/bin:$PATH
ENV GOPATH=/go
ENV CGO_ENABLED=1

FROM base AS linux
FROM base AS windows
# Set variables used by cni script which would otherwise shell out to powershell
ARG GATEWAY
ARG PREFIX_LEN

FROM ${TARGETOS} AS target
WORKDIR /go/src/github.com/containerd/containerd
COPY . .
ARG TARGETPLATFORM
ARG RELEASE_VER
ENV VERSION=$RELEASE_VER
RUN \
	--mount=type=bind,from=go,source=/usr/local/go,target=/usr/local/go \
	--mount=type=cache,target=/root/.cache/go-build \
	--mount=type=cache,target=/go/pkg \
	export CC=$(xx-info)-gcc && xx-go --wrap && \
	make release static-release && \
	for f in $(find bin -executable -type f); do xx-verify $f; done

# check git working tree after build
RUN \
	export GIT_STATUS_OUTPUT=$(git status --porcelain) && \
	test -z $GIT_STATUS_OUTPUT || (echo "repository contains uncommitted changes" && exit 1)

FROM scratch AS release
COPY --from=target /go/src/github.com/containerd/containerd/releases/ /
