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

ROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"/..
API_ROOT="${ROOT}/${API_PATH-"pkg/api/v1"}"

go get k8s.io/code-generator/cmd/go-to-protobuf/protoc-gen-gogo
if ! which protoc-gen-gogo >/dev/null; then
  echo "GOPATH is not in PATH"
  exit 1
fi

function cleanup {
  rm -f ${API_ROOT}/api.pb.go.bak
}

trap cleanup EXIT

protoc \
  --proto_path="${API_ROOT}" \
  --proto_path="${ROOT}/vendor" \
  --gogo_out=plugins=grpc:${API_ROOT} ${API_ROOT}/api.proto

# Update boilerplate for the generated file.
echo "$(cat hack/boilerplate/boilerplate ${API_ROOT}/api.pb.go)" > ${API_ROOT}/api.pb.go

gofmt -l -s -w ${API_ROOT}/api.pb.go
