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

GO ?= go
EPOCH_TEST_COMMIT ?= f9e02affccd51702191e5312665a16045ffef8ab
PROJECT := github.com/kubernetes-incubator/cri-containerd
BINDIR ?= ${DESTDIR}/usr/local/bin
BUILD_DIR ?= _output
# VERSION is derived from the current tag for HEAD plus amends. Version is used
# to set/overide the criContainerdVersion variable in the verison package for
# cri-containerd.
VERSION := $(shell git describe --tags --dirty)
# strip the first char of the tag if it's a `v`
VERSION := $(VERSION:v%=%)
BUILD_TAGS:= -ldflags '-X $(PROJECT)/pkg/version.criContainerdVersion=$(VERSION)'

all: binaries

default: help

help:
	@echo "Usage: make <target>"
	@echo
	@echo " * 'install'       - Install binaries to system locations"
	@echo " * 'binaries'      - Build cri-containerd"
	@echo " * 'test'          - Test cri-containerd"
	@echo " * 'test-cri'      - Test cri-containerd with cri validation test"
	@echo " * 'clean'         - Clean artifacts"
	@echo " * 'verify'        - Execute the source code verification tools"
	@echo " * 'install.tools' - Install tools used by verify"
	@echo " * 'install.deps'  - Install dependencies of cri-containerd (containerd, runc, cni)"
	@echo " * 'uninstall'     - Remove installed binaries from system locations"
	@echo " * 'version'       - Print current cri-containerd release version"

.PHONY: check-gopath

check-gopath:
ifndef GOPATH
	$(error GOPATH is not set)
endif

verify: lint gofmt boiler

version:
	@echo $(VERSION)

lint: check-gopath
	@echo "checking lint"
	@./hack/verify-lint.sh

gofmt:
	@echo "checking gofmt"
	@./hack/verify-gofmt.sh

boiler:
	@echo "checking boilerplate"
	@./hack/verify-boilerplate.sh

cri-containerd: check-gopath
	$(GO) build -o $(BUILD_DIR)/$@ \
	   $(BUILD_TAGS) \
	   $(GO_LDFLAGS) $(GO_GCFLAGS) \
	   $(PROJECT)/cmd/cri-containerd

test:
	go test -timeout=10m -race ./pkg/... $(BUILD_TAGS) $(GO_LDFLAGS) $(GO_GCFLAGS)

test-cri:
	@./hack/test-cri.sh

clean:
	rm -f $(BUILD_DIR)/cri-containerd

binaries: cri-containerd

install: check-gopath
	install -D -m 755 $(BUILD_DIR)/cri-containerd $(BINDIR)/cri-containerd

uninstall:
	rm -f $(BINDIR)/cri-containerd

.PHONY: install.deps

install.deps:
	@./hack/install-deps.sh

.PHONY: .gitvalidation
# When this is running in travis, it will only check the travis commit range.
# When running outside travis, it will check from $(EPOCH_TEST_COMMIT)..HEAD.
.gitvalidation: check-gopath
ifeq ($(TRAVIS),true)
	git-validation -q -run DCO,short-subject
else
	git-validation -v -run DCO,short-subject -range $(EPOCH_TEST_COMMIT)..HEAD
endif

.PHONY: install.tools .install.gitvalidation .install.gometalinter

install.tools: .install.gitvalidation .install.gometalinter

.install.gitvalidation:
	go get -u github.com/vbatts/git-validation

.install.gometalinter:
	go get -u github.com/alecthomas/gometalinter
	gometalinter --install

.PHONY: \
	binaries \
	boiler \
	clean \
	default \
	gofmt \
	help \
	install \
	lint \
	test \
	test-cri \
	uninstall \
	version
