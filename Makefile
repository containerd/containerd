GO ?= go
EPOCH_TEST_COMMIT ?= f2925f58acc259c4b894353f5fc404bdeb40028e
PROJECT := github.com/kubernetes-incubator/cri-containerd
BINDIR ?= ${DESTDIR}/usr/local/bin
BUILD_DIR ?= _output

all: binaries

default: help

help:
	@echo "Usage: make <target>"
	@echo
	@echo " * 'install'       - Install binaries to system locations"
	@echo " * 'binaries'      - Build cri-containerd"
	@echo " * 'clean'         - Clean artifacts"
	@echo " * 'verify'        - Execute the source code verification tools"
	@echo " * 'install.tools' - Installs tools used by verify"
	@echo " * 'uninstall'     - Remove installed binaries from system locations"

.PHONY: check-gopath

check-gopath:
ifndef GOPATH
	$(error GOPATH is not set)
endif

verify: lint gofmt

lint: check-gopath
	@echo "checking lint"
	@./hack/lint.sh

gofmt:
	@echo "checking gofmt"
	@./hack/verify-gofmt.sh

cri-containerd: check-gopath
	$(GO) build -o $(BUILD_DIR)/$@ \
		$(PROJECT)/cmd/cri-containerd

clean:
	rm -f $(BUILD_DIR)/cri-containerd

binaries: cri-containerd

install: check-gopath
	install -D -m 755 $(BUILD_DIR)/cri-containerd $(BINDIR)/cri-containerd

uninstall:
	rm -f $(BINDIR)/cri-containerd

.PHONY: .gitvalidation
# When this is running in travis, it will only check the travis commit range
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
	clean \
	default \
	gofmt \
	help \
	install \
	lint \
	uninstall
