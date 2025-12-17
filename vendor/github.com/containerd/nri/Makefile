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

GO_CMD     := go
GO_BUILD   := $(GO_CMD) build
GO_INSTALL := $(GO_CMD) install
GO_TEST    := $(GO_CMD) test
GO_LINT    := golint -set_exit_status
GO_FMT     := gofmt
GO_VET     := $(GO_CMD) vet

GO_BUILD_FLAGS ?=

GO_MODULES := $(shell $(GO_CMD) list ./...)

GOLANG_CILINT := golangci-lint
GINKGO        := ginkgo

RESOLVED_PWD  := $(shell realpath $(shell pwd))
BUILD_PATH    := $(RESOLVED_PWD)/build
BIN_PATH      := $(BUILD_PATH)/bin
TOOLS_PATH    := $(BUILD_PATH)/tools
PROTOC_PATH   := $(TOOLS_PATH)/protoc
COVERAGE_PATH := $(BUILD_PATH)/coverage

PROTOBUF_VERSION = 3.20.1
PROTO_SOURCES = $(shell find pkg -name '*.proto' | grep -v /vendor/)
PROTO_INCLUDE = -I $(PWD) -I$(PROTOC_PATH)/include
PROTO_OPTIONS = --proto_path=. $(PROTO_INCLUDE) \
    --go_opt=paths=source_relative --go_out=. \
    --go-ttrpc_opt=paths=source_relative --go-ttrpc_out=. \
    --go-plugin_opt=paths=source_relative,disable_pb_gen=true --go-plugin_out=.
PROTO_COMPILE = PATH=$(PROTOC_PATH)/bin protoc $(PROTO_OPTIONS)

PLUGINS := \
	$(BIN_PATH)/logger \
	$(BIN_PATH)/device-injector \
	$(BIN_PATH)/hook-injector \
	$(BIN_PATH)/differ \
	$(BIN_PATH)/ulimit-adjuster \
	$(BIN_PATH)/v010-adapter \
	$(BIN_PATH)/template \
	$(BIN_PATH)/wasm \
	$(BIN_PATH)/network-device-injector \
	$(BIN_PATH)/network-logger \
	$(BIN_PATH)/rdt

ifneq ($(V),1)
  Q := @
endif

#
# top-level targets
#

all: build build-plugins

build: build-proto build-check

clean: clean-plugins

allclean: clean clean-cache

test: test-gopkgs

FORCE:

#
# build targets
#

build-proto: check-protoc install-ttrpc-plugin install-wasm-plugin install-protoc-dependencies
	for src in $(PROTO_SOURCES); do \
		$(PROTO_COMPILE) $$src; \
	done
	sed -i '1s;^;//go:build !wasip1\n\n;' pkg/api/api_ttrpc.pb.go

.PHONY: build-proto-dockerized
build-proto-dockerized:
	$(Q)docker build --build-arg ARTIFACTS="$(dir $(PROTO_SOURCES))" --target final \
		--output type=local,dest=$(RESOLVED_PWD) \
		-f hack/Dockerfile.buildproto .
	$(Q)tar xf artifacts.tgz && rm -f artifacts.tgz

build-plugins: $(PLUGINS)

build-check:
	$(Q)$(GO_BUILD) -v $(GO_MODULES)

mod-tidy:
	$(Q)$(GO_CMD) mod tidy
	$(Q)./scripts/go-mod-tidy

#
# clean targets
#

clean-plugins:
	$(Q)rm -f $(PLUGINS)

clean-cache:
	$(Q)$(GO_CMD) clean -cache -testcache

#
# plugins build targets
#

$(BIN_PATH)/% build/bin/%: FORCE
	$(Q)echo "Building $@..."; \
	$(GO_BUILD) -C plugins/$* -o $(abspath $@) $(GO_BUILD_FLAGS) .

$(BIN_PATH)/wasm build/bin/wasm: FORCE
	$(Q)echo "Building $@..."; \
	mkdir -p $(BIN_PATH) && \
	GOOS=wasip1 GOARCH=wasm $(GO_BUILD) -C plugins/wasm -o $(abspath $@) $(GO_BUILD_FLAGS) -buildmode=c-shared .

#
# test targets
#

test-gopkgs: ginkgo-tests test-ulimits test-rdt

SKIPPED_PKGS="ulimit-adjuster,device-injector,rdt"

ginkgo-tests:
	$(Q)$(GINKGO) run \
	    --race \
	    --trace \
	    --cover \
	    --covermode atomic \
	    --output-dir $(COVERAGE_PATH) \
	    --junit-report junit.xml \
	    --coverprofile coverprofile \
	    --succinct \
	    --skip-package $(SKIPPED_PKGS) \
	    -r && \
	$(GO_CMD) tool cover -html=$(COVERAGE_PATH)/coverprofile -o $(COVERAGE_PATH)/coverage.html

test-ulimits:
	$(Q)cd ./plugins/ulimit-adjuster && $(GO_TEST) -v

test-device-injector:
	$(Q)cd ./plugins/device-injector && $(GO_TEST) -v

test-rdt:
	$(Q)cd ./plugins/rdt && $(GO_TEST) -v

codecov: SHELL := $(shell which bash)
codecov:
	bash <(curl -s https://codecov.io/bash) -f $(COVERAGE_PATH)/coverprofile

#
# other validation targets
#

fmt format:
	$(Q)$(GO_FMT) -s -d -e .

lint:
	$(Q)$(GO_LINT) -set_exit_status ./...

vet:
	$(Q)$(GO_VET) ./...

golangci-lint:
	$(Q)$(GOLANG_CILINT) run

validate-repo-no-changes:
	$(Q)test -z "$$(git status --short | tee /dev/stderr)" || { \
		echo "Repository has changes."; \
		echo "Please make sure to commit all changes, including generated files."; \
		exit 1; \
	}

#
# targets for installing dependencies
#
check-protoc:
	$(Q)found_proto=$(shell $(PROTO_COMPILE) --version 2> /dev/null | awk '{print $$2}'); \
	if [ "$$found_proto" != "$(PROTOBUF_VERSION)" ]; then \
	echo "installing protoc version $(PROTOBUF_VERSION) (found: $$found_proto)"; \
		make clean-protoc; \
		make install-protoc; \
	else \
		echo "protoc version $$found_proto found."; \
	fi

install-protoc install-protobuf:
	$(Q)PROTOBUF_VERSION=$(PROTOBUF_VERSION) INSTALL_DIR=$(PROTOC_PATH) ./scripts/install-protobuf

clean-protoc:
	$(Q)rm -rf $(PROTOC_PATH)

install-ttrpc-plugin:
	$(Q)GOBIN="$(PROTOC_PATH)/bin" $(GO_INSTALL) github.com/containerd/ttrpc/cmd/protoc-gen-go-ttrpc

install-wasm-plugin:
	$(Q)GOBIN="$(PROTOC_PATH)/bin" $(GO_INSTALL) github.com/knqyf263/go-plugin/cmd/protoc-gen-go-plugin

install-protoc-dependencies:
	$(Q)GOBIN="$(PROTOC_PATH)/bin" $(GO_INSTALL) google.golang.org/protobuf/cmd/protoc-gen-go

install-ginkgo:
	$(Q)$(GO_INSTALL) -mod=mod github.com/onsi/ginkgo/v2/ginkgo
