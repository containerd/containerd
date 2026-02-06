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

# Fix for git 'dubious ownership' error inside Docker
if [ -d "$SRC/containerd" ]; then
    git config --global --add safe.directory $SRC/containerd
fi

IFS=$'
'

compile_fuzzers() {
    local regex=$1
    local compile_fuzzer=$2
    local blocklist=$3

    # Exclude docs directory from the fuzzer search
    local exclude_docs_regex='docs/'

    for line in $(git grep --full-name "$regex" | grep -v -E "$blocklist" | grep -v -E "$exclude_docs_regex"); do
        if [[ "$line" =~ (.*)/.*:.*(Fuzz[A-Za-z0-9]+) ]]; then
            local pkg=${BASH_REMATCH[1]}
            local func=${BASH_REMATCH[2]}
            "$compile_fuzzer" "github.com/containerd/containerd/v2/$pkg" "$func" "fuzz_$func"
        else
            echo "failed to parse: $line"
            exit 1
        fi
    done
}

cd $SRC/containerd

# Clean up any leftover temporary files from previous build attempts
rm -f client/registerfuzzdep.go

# Add temporary CXXFLAGS
OLDCXXFLAGS=$CXXFLAGS
export CXXFLAGS="$CXXFLAGS -lresolv"

# Change path of socket since OSS-fuzz does not grant access to /run
sed -i 's/\/run\/containerd/\/tmp\/containerd/g' $SRC/containerd/defaults/defaults_unix.go

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
if [ ! -d "runc" ]; then
    git clone https://github.com/opencontainers/runc --branch release-1.1
fi
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

compile_fuzzers '^func FuzzInteg.*testing\.F' compile_native_go_fuzzer vendor

cp $SRC/containerd/contrib/fuzz/*.options $OUT/
cp $SRC/containerd/contrib/fuzz/*.dict $OUT/

# Resume CXXFLAGS
export CXXFLAGS=$OLDCXXFLAGS
