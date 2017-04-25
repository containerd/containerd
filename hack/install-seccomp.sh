#!/bin/bash

set -e
set -x

export SECCOMP_PATH="$(mktemp -d)"

curl -fsSL "https://github.com/seccomp/libseccomp/releases/download/v${SECCOMP_VERSION}/libseccomp-${SECCOMP_VERSION}.tar.gz" \
	| tar -xzC "$SECCOMP_PATH" --strip-components=1
(
    cd "$SECCOMP_PATH"
	./configure --prefix=/usr/local
	make
	sudo make install
	sudo ldconfig
)
rm -rf "$SECCOMP_PATH"
