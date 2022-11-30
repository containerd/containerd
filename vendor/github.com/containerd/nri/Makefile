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

PROTO_SOURCES = $(shell find . -name '*.proto' | grep -v /vendor/)
PROTO_GOFILES = $(patsubst %.proto,%.pb.go,$(PROTO_SOURCES))
PROTO_INCLUDE = -I$(PWD):/usr/local/include:/usr/include
PROTO_OPTIONS = --proto_path=. $(PROTO_INCLUDE) \
    --go_opt=paths=source_relative --go_out=. \
    --go-ttrpc_opt=paths=source_relative --go-ttrpc_out=.
PROTO_COMPILE = PATH=$(PATH):$(shell go env GOPATH)/bin; protoc $(PROTO_OPTIONS)

GO_CMD     := go
GO_BUILD   := $(GO_CMD) build
GO_INSTALL := $(GO_CMD) install
GO_TEST    := $(GO_CMD) test
GO_LINT    := golint -set_exit_status
GO_FMT     := gofmt
GO_VET     := $(GO_CMD) vet

GO_MODULES := $(shell $(GO_CMD) list ./...)

GOLANG_CILINT := golangci-lint
GINKGO        := ginkgo

BUILD_PATH    := $(shell pwd)/build
BIN_PATH      := $(BUILD_PATH)/bin
COVERAGE_PATH := $(BUILD_PATH)/coverage

PLUGINS := \
	$(BIN_PATH)/logger \
	$(BIN_PATH)/device-injector \
	$(BIN_PATH)/hook-injector \
	$(BIN_PATH)/differ \
	$(BIN_PATH)/v010-adapter \
	$(BIN_PATH)/template


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

#
# build targets
#

build-proto: $(PROTO_GOFILES)

build-plugins: $(PLUGINS)

build-check:
	$(Q)$(GO_BUILD) -v $(GO_MODULES)

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

$(BIN_PATH)/logger: $(wildcard plugins/logger/*.go)
	$(Q)echo "Building $@..."; \
	cd $(dir $<) && $(GO_BUILD) -o $@ .

$(BIN_PATH)/device-injector: $(wildcard plugins/device-injector/*.go)
	$(Q)echo "Building $@..."; \
	cd $(dir $<) && $(GO_BUILD) -o $@ .

$(BIN_PATH)/hook-injector: $(wildcard plugins/hook-injector/*.go)
	$(Q)echo "Building $@..."; \
	cd $(dir $<) && $(GO_BUILD) -o $@ .

$(BIN_PATH)/differ: $(wildcard plugins/differ/*.go)
	$(Q)echo "Building $@..."; \
	cd $(dir $<) && $(GO_BUILD) -o $@ .

$(BIN_PATH)/v010-adapter: $(wildcard plugins/v010-adapter/*.go)
	$(Q)echo "Building $@..."; \
	cd $(dir $<) && $(GO_BUILD) -o $@ .

$(BIN_PATH)/template: $(wildcard plugins/template/*.go)
	$(Q)echo "Building $@..."; \
	cd $(dir $<) && $(GO_BUILD) -o $@ .

#
# test targets
#

test-gopkgs: ginkgo-tests

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
	    -r .; \
	$(GO_CMD) tool cover -html=$(COVERAGE_PATH)/coverprofile -o $(COVERAGE_PATH)/coverage.html

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

#
# proto generation targets
#

%.pb.go: %.proto
	$(Q)echo "Generating $@..."; \
	$(PROTO_COMPILE) $<

#
# targets for installing dependencies
#

install-protoc install-protobuf:
	$(Q)./scripts/install-protobuf && \

install-ttrpc-plugin:
	$(Q)$(GO_INSTALL) github.com/containerd/ttrpc/cmd/protoc-gen-go-ttrpc@74421d10189e8c118870d294c9f7f62db2d33ec1

install-protoc-dependencies:
	$(Q)$(GO_INSTALL) google.golang.org/protobuf/cmd/protoc-gen-go@v1.28.0

install-ginkgo:
	$(Q)$(GO_INSTALL) -mod=mod github.com/onsi/ginkgo/v2/ginkgo
