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

FROM golang:1.10 AS proto3
RUN apt-get update && apt-get install -y autoconf automake g++ libtool unzip
COPY script/setup/install-protobuf install-protobuf
RUN ./install-protobuf

# Install runc
FROM golang:1.10 AS runc
RUN apt-get update && apt-get install -y curl libseccomp-dev
COPY vendor.conf /go/src/github.com/containerd/containerd/vendor.conf
COPY script/setup/install-runc install-runc
RUN ./install-runc

FROM golang:1.10 AS build
RUN apt-get update && apt-get install -y btrfs-tools gcc git libseccomp-dev make xfsprogs

COPY --from=proto3 /usr/local/bin/protoc /usr/local/bin/protoc
COPY --from=proto3 /usr/local/include/google /usr/local/include/google

COPY . /go/src/github.com/containerd/containerd

WORKDIR /go/src/github.com/containerd/containerd
RUN make

FROM scratch
COPY --from=build /go/src/github.com/containerd/containerd/bin/* /bin/
COPY --from=runc /usr/local/sbin/runc /bin/runc
