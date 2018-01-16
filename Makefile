# Copyright 2017 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

GO := go
GOOS := $(shell $(GO) env GOOS)
GOARCH := $(shell $(GO) env GOARCH)
EPOCH_TEST_COMMIT := f9e02affccd51702191e5312665a16045ffef8ab
PROJECT := github.com/containerd/cri-containerd
BINDIR := ${DESTDIR}/usr/local/bin
BUILD_DIR := _output
# VERSION is derived from the current tag for HEAD plus amends. Version is used
# to set/overide the CRIContainerdVersion variable in the verison package for
# cri-containerd.
VERSION := $(shell git describe --tags --dirty)
# strip the first char of the tag if it's a `v`
VERSION := $(VERSION:v%=%)
TARBALL_PREFIX := cri-containerd
TARBALL := $(TARBALL_PREFIX)-$(VERSION).$(GOOS)-$(GOARCH).tar.gz
BUILD_TAGS := seccomp apparmor
GO_LDFLAGS := -X $(PROJECT)/pkg/version.CRIContainerdVersion=$(VERSION)
SOURCES := $(shell find cmd/ pkg/ vendor/ -name '*.go')
PLUGIN_SOURCES := $(shell ls *.go)
INTEGRATION_SOURCES := $(shell find integration/ -name '*.go')

all: binaries

default: help

help:
	@echo "Usage: make <target>"
	@echo
	@echo " * 'install'          - Install binaries to system locations"
	@echo " * 'binaries'         - Build cri-containerd"
	@echo " * 'plugin'           - Build cri-containerd as a plugin package"
	@echo " * 'static-binaries   - Build static cri-containerd"
	@echo " * 'release'          - Build release tarball"
	@echo " * 'push'             - Push release tarball to GCS"
	@echo " * 'test'             - Test cri-containerd with unit test"
	@echo " * 'test-integration' - Test cri-containerd with integration test"
	@echo " * 'test-cri'         - Test cri-containerd with cri validation test"
	@echo " * 'test-e2e-node'    - Test cri-containerd with Kubernetes node e2e test"
	@echo " * 'clean'            - Clean artifacts"
	@echo " * 'verify'           - Execute the source code verification tools"
	@echo " * 'proto'            - Update protobuf of cri-containerd api"
	@echo " * 'install.tools'    - Install tools used by verify"
	@echo " * 'install.deps'     - Install dependencies of cri-containerd (containerd, runc, cni) Note: BUILDTAGS defaults to 'seccomp apparmor' for runc build"
	@echo " * 'uninstall'        - Remove installed binaries from system locations"
	@echo " * 'version'          - Print current cri-containerd release version"

verify: lint gofmt boiler deps-version

version:
	@echo $(VERSION)

lint:
	@echo "checking lint"
	@./hack/verify-lint.sh

gofmt:
	@echo "checking gofmt"
	@./hack/verify-gofmt.sh

boiler:
	@echo "checking boilerplate"
	@./hack/verify-boilerplate.sh

deps-version:
	@echo "checking /hack/versions"
	@./hack/update-vendor.sh -only-verify

$(BUILD_DIR)/cri-containerd: $(SOURCES)
	$(GO) build -o $@ \
		-tags '$(BUILD_TAGS)' \
		-ldflags '$(GO_LDFLAGS)' \
		-gcflags '$(GO_GCFLAGS)' \
		$(PROJECT)/cmd/cri-containerd

test:
	$(GO) test -timeout=10m -race ./pkg/... \
		-tags '$(BUILD_TAGS)' \
	        -ldflags '$(GO_LDFLAGS)' \
		-gcflags '$(GO_GCFLAGS)'

$(BUILD_DIR)/integration.test: $(INTEGRATION_SOURCES)
	$(GO) test -c $(PROJECT)/integration -o $(BUILD_DIR)/integration.test

test-integration: $(BUILD_DIR)/integration.test binaries
	@./hack/test-integration.sh

test-cri: binaries
	@./hack/test-cri.sh

test-e2e-node: binaries
	@VERSION=$(VERSION) ./hack/test-e2e-node.sh

clean:
	rm -rf $(BUILD_DIR)/*

binaries: $(BUILD_DIR)/cri-containerd

# TODO(random-liu): Make this only build when source files change and
# add this to target all.
plugin: $(PLUGIN_SOURCES) $(SOURCES)
	$(GO) build -tags '$(BUILD_TAGS)' \
		-ldflags '$(GO_LDFLAGS)' \
		-gcflags '$(GO_GCFLAGS)' \

static-binaries: GO_LDFLAGS += -extldflags "-fno-PIC -static"
static-binaries: $(BUILD_DIR)/cri-containerd

install: binaries
	install -D -m 755 $(BUILD_DIR)/cri-containerd $(BINDIR)/cri-containerd

uninstall:
	rm -f $(BINDIR)/cri-containerd

$(BUILD_DIR)/$(TARBALL): static-binaries hack/versions
	@BUILD_DIR=$(BUILD_DIR) TARBALL=$(TARBALL) ./hack/release.sh

release: $(BUILD_DIR)/$(TARBALL)

push: $(BUILD_DIR)/$(TARBALL)
	@BUILD_DIR=$(BUILD_DIR) TARBALL=$(TARBALL) VERSION=$(VERSION) ./hack/push.sh

proto:
	@hack/update-proto.sh

.PHONY: install.deps

install.deps:
	@./hack/install-deps.sh

.PHONY: .gitvalidation
# When this is running in travis, it will only check the travis commit range.
# When running outside travis, it will check from $(EPOCH_TEST_COMMIT)..HEAD.
.gitvalidation:
ifeq ($(TRAVIS),true)
	git-validation -q -run DCO,short-subject
else
	git-validation -v -run DCO,short-subject -range $(EPOCH_TEST_COMMIT)..HEAD
endif

.PHONY: install.tools .install.gitvalidation .install.gometalinter

install.tools: .install.gitvalidation .install.gometalinter

.install.gitvalidation:
	$(GO) get -u github.com/vbatts/git-validation

.install.gometalinter:
	$(GO) get -u github.com/alecthomas/gometalinter
	gometalinter --install

.PHONY: \
	binaries \
	static-binaries \
	plugin \
	release \
	push \
	boiler \
	clean \
	default \
	gofmt \
	help \
	install \
	lint \
	test \
	test-integration \
	test-cri \
	test-e2e-node \
	uninstall \
	version \
	proto
