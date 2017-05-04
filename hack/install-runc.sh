#!/bin/bash

set -e

export GOPATH="$(mktemp -d)"
git clone git://github.com/opencontainers/runc.git "$GOPATH/src/github.com/opencontainers/runc"
cd "$GOPATH/src/github.com/opencontainers/runc"
git checkout -q "$RUNC_COMMIT"
make BUILDTAGS="seccomp apparmor selinux"
sudo make install
