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

IFS=$'\n'

compile_fuzzers() {
    local regex=$1
    local compile_fuzzer=$2

    for line in $(git grep "$regex" | grep -v vendor)
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
wget --quiet https://go.dev/dl/go1.18.3.linux-amd64.tar.gz

mkdir temp-go
rm -rf /root/.go/*
tar -C temp-go/ -xzf go1.18.3.linux-amd64.tar.gz
mv temp-go/go/* /root/.go/
cd $SRC/containerd

go mod tidy
rm vendor/github.com/cilium/ebpf/internal/btf/fuzz.go
rm /root/go/pkg/mod/github.com/cilium/ebpf@v0.7.0/internal/btf/fuzz.go

cd "$(dirname "${BASH_SOURCE[0]}")"
cd ../../

# Move all fuzzers that don't have the "fuzz" package out of this dir
mv contrib/fuzz/docker_fuzzer.go remotes/docker/
mv contrib/fuzz/container_fuzzer.go integration/client/

rm -r vendor


# Change path of socket since OSS-fuzz does not grant access to /run
sed -i 's/\/run\/containerd/\/tmp\/containerd/g' $SRC/containerd/defaults/defaults_unix.go

# To build FuzzContainer2 we need to prepare a few things:
# We change the name of the cmd/containerd package
# so that we can import it.
# We furthermore add an exported function that is similar
# to cmd/containerd.main and call that instead of calling
# the containerd binary.
#
# In the fuzzer we import cmd/containerd as a low-maintenance
# way of initializing all the plugins.
# Make backup of cmd/containerd:
cp -r $SRC/containerd/cmd/containerd $SRC/cmd-containerd-backup
# Rename package:
find $SRC/containerd/cmd/containerd -type f -exec sed -i 's/package main/package mainfuzz/g' {} \;
# Add an exported function
sed -i -e '$afunc StartDaemonForFuzzing(arguments []string) {\n\tapp := App()\n\t_ = app.Run(arguments)\n}' $SRC/containerd/cmd/containerd/command/main.go
# Build fuzzer:
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzContainerdImport fuzz_containerd_import
# Reinstante backup of cmd/containerd:
mv $SRC/cmd-containerd-backup $SRC/containerd/cmd/containerd

# Compile more fuzzers
mv $SRC/containerd/filters/filter_test.go $SRC/containerd/filters/filter_test_fuzz.go
go get github.com/AdamKorcz/go-118-fuzz-build/utils

compile_fuzzers '^func Fuzz.*testing\.F' compile_native_go_fuzzer
compile_fuzzers '^func Fuzz.*data' compile_go_fuzzer

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
compile_go_fuzzer github.com/containerd/containerd/integration/client FuzzCreateContainerNoTearDown fuzz_create_container_no_teardown
compile_go_fuzzer github.com/containerd/containerd/integration/client FuzzCreateContainerWithTearDown fuzz_create_container_with_teardown
compile_go_fuzzer github.com/containerd/containerd/integration/client FuzzNoTearDownWithDownload fuzz_no_teardown_with_download
