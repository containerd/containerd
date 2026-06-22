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


# Base path used to install.
DESTDIR ?= /usr/local

VERSION=$(shell git describe --match 'v[0-9]*' --dirty='.m' --always)

CTR_LDFLAGS=-ldflags '-X github.com/containerd/containerd/v2/version.Version=$(VERSION)'
COMMANDS=ctd-decoder ctr-enc
RELEASE_COMMANDS=ctd-decoder

BINARIES=$(addprefix bin/,$(COMMANDS))
RELEASE_BINARIES=$(addprefix bin/,$(RELEASE_COMMANDS))

.PHONY: check build ctd-decoder

all: build

build: $(BINARIES)

FORCE:

bin/ctd-decoder: cmd/ctd-decoder FORCE
	cd cmd && go build -o ../$@ -v ./ctd-decoder/

bin/ctr-enc: cmd/ctr FORCE
	cd cmd && go build -o ../$@ ${CTR_LDFLAGS} -v ./ctr/

check:
	@echo "$@"
	@golangci-lint run
	@script/check_format.sh

install:
	@echo "$@"
	@mkdir -p $(DESTDIR)/bin
	@install $(BINARIES) $(DESTDIR)/bin

containerd-release:
	@echo "$@"
	@mkdir -p $(DESTDIR)/bin
	@install $(RELEASE_BINARIES) $(DESTDIR)/bin

uninstall:
	@echo "$@"
	@rm -f $(addprefix $(DESTDIR)/bin/,$(notdir $(BINARIES)))

clean:
	@echo "$@"
	@rm -f $(BINARIES)

test:
	@echo "$@"
	@go test ./...
