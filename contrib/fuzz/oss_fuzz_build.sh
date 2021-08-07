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

cd "$(dirname "${BASH_SOURCE[0]}")"
cd ../../

# Move all fuzzers that don't have the "fuzz" package out of this dir
mv contrib/fuzz/docker_fuzzer.go remotes/docker/
mv contrib/fuzz/container_fuzzer.go integration/client/


compile_go_fuzzer github.com/containerd/containerd/remotes/docker FuzzFetcher fuzz_fetcher
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzFiltersParse fuzz_filters_parse
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzPlatformsParse fuzz_platforms_parse
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzApply fuzz_apply
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzImportIndex fuzz_import_index
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzCSWalk fuzz_cs_walk

# FuzzCreateContainer requires more setup than the fuzzers above.
# We need the binaries from "make".
wget -c https://github.com/protocolbuffers/protobuf/releases/download/v3.11.4/protoc-3.11.4-linux-x86_64.zip
unzip protoc-3.11.4-linux-x86_64.zip -d /usr/local

export CGO_ENABLED=1
export GOARCH=amd64

# Build runc
cd $SRC/
git clone https://github.com/opencontainers/runc
cd runc
make
make install

# Build static containerd
cd $SRC/containerd
make EXTRA_FLAGS="-buildmode pie" \
	EXTRA_LDFLAGS='-linkmode external -extldflags "-fno-PIC -static"' \
	BUILDTAGS="netgo osusergo static_build"


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
compile_go_fuzzer . FuzzCreateContainerNoTearDown fuzz_create_container_no_teardown
compile_go_fuzzer . FuzzCreateContainerWithTearDown fuzz_create_container_with_teardown
compile_go_fuzzer . FuzzNoTearDownWithDownload fuzz_no_teardown_with_download
