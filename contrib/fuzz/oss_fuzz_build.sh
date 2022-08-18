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
set -o nounset
set -o pipefail
set -o errexit
set -x

IFS=$'\n'

compile_fuzzers() {
    local regex=$1
    local compile_fuzzer=$2
    local blocklist=$3

    for line in $(git grep --full-name "$regex" | grep -v -E "$blocklist")
    do
        if [[ "$line" =~ (.*)/.*:.*(Fuzz[A-Za-z0-9]+) ]]; then
            local pkg=${BASH_REMATCH[1]}
            local func=${BASH_REMATCH[2]}
            "$compile_fuzzer" "github.com/containerd/containerd/$pkg" "$func" "fuzz_$func"
        else
            echo "failed to parse: $line"
            exit 1
        fi
    done
}

apt-get update && apt-get install -y wget
cd $SRC
wget --quiet https://go.dev/dl/go1.19.linux-amd64.tar.gz

mkdir temp-go
rm -rf /root/.go/*
tar -C temp-go/ -xzf go1.19.linux-amd64.tar.gz
mv temp-go/go/* /root/.go/
cd $SRC/containerd

go mod tidy

cd "$(dirname "${BASH_SOURCE[0]}")"
cd ../../

rm -r vendor

# Change path of socket since OSS-fuzz does not grant access to /run
sed -i 's/\/run\/containerd/\/tmp\/containerd/g' $SRC/containerd/defaults/defaults_unix.go

go get github.com/AdamKorcz/go-118-fuzz-build/utils

compile_fuzzers '^func Fuzz.*testing\.F' compile_native_go_fuzzer vendor
compile_fuzzers '^func Fuzz.*data' compile_go_fuzzer '(vendor|Integ)'

# The below fuzzers require more setup than the fuzzers above.
# We need the binaries from "make".
wget --quiet https://github.com/protocolbuffers/protobuf/releases/download/v3.11.4/protoc-3.11.4-linux-x86_64.zip
unzip protoc-3.11.4-linux-x86_64.zip -d /usr/local

export CGO_ENABLED=1
export GOARCH=amd64

# Build runc
cd $SRC/
git clone https://github.com/opencontainers/runc --branch release-1.0
cd runc
make
make install

# Build static containerd
cd $SRC/containerd
make STATIC=1

mkdir $OUT/containerd-binaries || true
cd $SRC/containerd/bin && cp * $OUT/containerd-binaries/ && cd -

# Change defaultState and defaultAddress fron /run/containerd-test to /tmp/containerd-test:
sed -i 's/\/run\/containerd-test/\/tmp\/containerd-test/g' $SRC/containerd/integration/client/client_unix_test.go

cd integration/client

# Rename all *_test.go to *_test_fuzz.go to use their declarations:
for i in $( ls *_test.go ); do mv $i ./${i%.*}_fuzz.go; done

# Remove windows test to avoid double declarations:
rm ./client_windows_test_fuzz.go
rm ./helpers_windows_test_fuzz.go

compile_fuzzers '^func FuzzInteg.*data' compile_go_fuzzer vendor
