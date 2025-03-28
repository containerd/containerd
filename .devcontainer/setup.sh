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

set -eux

script/setup/install-seccomp
script/setup/install-runc
script/setup/install-cni
script/setup/install-critools
script/setup/install-failpoint-binaries
script/setup/install-gotestsum
script/setup/install-teststat

script/setup/install-protobuf \
    && mkdir -p /go/src/usr/local/bin /go/src/usr/local/include \
    && mv /usr/local/bin/protoc /go/src/usr/local/bin/protoc \
    && mv /usr/local/include/google /go/src/usr/local/include/google

make binaries GO_BUILD_FLAGS="-mod=vendor"
sudo -E PATH=$PATH make install
