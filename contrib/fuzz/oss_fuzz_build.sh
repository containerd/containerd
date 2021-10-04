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
compile_go_fuzzer github.com/containerd/containerd/remotes/docker FuzzFetcher fuzz_fetcher
compile_go_fuzzer github.com/containerd/containerd/remotes/docker FuzzParseDockerRef fuzz_parse_docker_ref
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzFiltersParse fuzz_filters_parse
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzPlatformsParse fuzz_platforms_parse
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzApply fuzz_apply
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzImportIndex fuzz_import_index
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzCSWalk fuzz_cs_walk
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzArchiveExport fuzz_archive_export
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzParseAuth fuzz_parse_auth
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzParseProcPIDStatus fuzz_parse_proc_pid_status
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzImageStore fuzz_image_store
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzLeaseManager fuzz_lease_manager
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzContainerStore fuzz_container_store
compile_go_fuzzer github.com/containerd/containerd/contrib/fuzz FuzzContentStore fuzz_content_store


# The below fuzzers require more setup than the fuzzers above.
# We need the binaries from "make".
wget -c https://github.com/protocolbuffers/protobuf/releases/download/v3.11.4/protoc-3.11.4-linux-x86_64.zip
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
compile_go_fuzzer github.com/containerd/containerd/integration/client FuzzCreateContainerNoTearDown fuzz_create_container_no_teardown
compile_go_fuzzer github.com/containerd/containerd/integration/client FuzzCreateContainerWithTearDown fuzz_create_container_with_teardown
compile_go_fuzzer github.com/containerd/containerd/integration/client FuzzNoTearDownWithDownload fuzz_no_teardown_with_download
