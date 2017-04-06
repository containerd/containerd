# Root directory of the project (absolute path).
ROOTDIR=$(dir $(abspath $(lastword $(MAKEFILE_LIST))))

# Base path used to install.
DESTDIR=/usr/local

# Used to populate version variable in main package.
VERSION=$(shell git describe --match 'v[0-9]*' --dirty='.m' --always)

PKG=github.com/containerd/containerd

# Project packages.
PACKAGES=$(shell go list ./... | grep -v /vendor/)
INTEGRATION_PACKAGE=${PKG}/integration
SNAPSHOT_PACKAGES=$(shell go list ./snapshot/...)

# Project binaries.
UNIXCOMMANDS=ctr containerd containerd-shim protoc-gen-gogoctrd dist ctrd-protobuild
WINDOWSCOMMANDS=containerd.exe
UNIXBINARIES=$(addprefix bin/,$(UNIXCOMMANDS))
WINDOWSBINARIES=$(addprefix bin/,$(WINDOWSCOMMANDS))

GO_LDFLAGS=-ldflags "-X $(PKG).Version=$(VERSION) -X $(PKG).Package=$(PKG)"

# Flags passed to `go test`
TESTFLAGS ?=-parallel 8 -race

.PHONY: clean all AUTHORS fmt vet lint dco build binaries test integration setup generate protos checkprotos coverage ci check help install uninstall vendor
.DEFAULT: default

all: unixbinaries windowsbinaries

check: fmt vet lint ineffassign ## run fmt, vet, lint, ineffassign

ci: check unixbinaries windowsbinaries checkprotos coverage coverage-integration ## to be used by the CI

AUTHORS: .mailmap .git/HEAD
	git log --format='%aN <%aE>' | sort -fu > $@

setup: ## install dependencies
	@echo "$@"
	# TODO(stevvooe): Install these from the vendor directory
	@go get -u github.com/golang/lint/golint
	#@go get -u github.com/kisielk/errcheck
	@go get -u github.com/gordonklaus/ineffassign

generate: protos
	@echo "$@"
	@PATH=${ROOTDIR}/bin:${PATH} go generate -x ${PACKAGES}

protos: bin/protoc-gen-gogoctrd bin/ctrd-protobuild ## generate protobuf
	@echo "$@"
	@PATH=${ROOTDIR}/bin:${PATH} ctrd-protobuild ${PACKAGES}

checkprotos: protos ## check if protobufs needs to be generated again
	@echo "$@"
	@test -z "$$(git status --short | grep ".pb.go" | tee /dev/stderr)" || \
		((git diff | cat) && \
		(echo "please run 'make generate' when making changes to proto files" && false))

# Depends on binaries because vet will silently fail if it can't load compiled
# imports
vet: unixbinaries windowsbinaries ## run go vet
	@echo "$@"
	@test -z "$$(go vet ${PACKAGES} 2>&1 | grep -v 'constant [0-9]* not a string in call to Errorf' | egrep -v '(timestamp_test.go|duration_test.go|exit status 1)' | tee /dev/stderr)"

fmt: ## run go fmt
	@echo "$@"
	@test -z "$$(gofmt -s -l . | grep -v vendor/ | grep -v ".pb.go$$" | tee /dev/stderr)" || \
		(echo "please format Go code with 'gofmt -s -w'" && false)
	@test -z "$$(find . -path ./vendor -prune -o ! -name timestamp.proto ! -name duration.proto -name '*.proto' -type f -exec grep -Hn -e "^ " {} \; | tee /dev/stderr)" || \
		(echo "please indent proto files with tabs only" && false)
	@test -z "$$(find . -path ./vendor -prune -o -name '*.proto' -type f -exec grep -EHn "[_ ]id = " {} \; | grep -v gogoproto.customname | tee /dev/stderr)" || \
		(echo "id fields in proto files must have a gogoproto.customname set" && false)
	@test -z "$$(find . -path ./vendor -prune -o -name '*.proto' -type f -exec grep -Hn "Meta meta = " {} \; | grep -v '(gogoproto.nullable) = false' | tee /dev/stderr)" || \
		(echo "meta fields in proto files must have option (gogoproto.nullable) = false" && false)

lint: ## run go lint
	@echo "$@"
	@test -z "$$(golint ./... | grep -v vendor/ | grep -v ".pb.go:" | tee /dev/stderr)"

dco: ## dco check
	@which git-validation > /dev/null 2>/dev/null || (echo "ERROR: git-validation not found" && false)
ifdef TRAVIS_COMMIT_RANGE
	git-validation -q -run DCO,short-subject,dangling-whitespace
else
	git-validation -v -run DCO,short-subject,dangling-whitespace -range $(EPOCH_TEST_COMMIT)..HEAD
endif

ineffassign: ## run ineffassign
	@echo "$@"
	@test -z "$$(ineffassign . | grep -v vendor/ | grep -v ".pb.go:" | tee /dev/stderr)"

#errcheck: ## run go errcheck
#	@echo "$@"
#	@test -z "$$(errcheck ./... | grep -v vendor/ | grep -v ".pb.go:" | tee /dev/stderr)"

build: ## build the go packages
	@echo "$@"
	@go build -i -v ${GO_LDFLAGS} ${GO_GCFLAGS} ${PACKAGES}

test: ## run tests, except integration tests and tests that require root
	@echo "$@"
	@go test ${TESTFLAGS} $(filter-out ${INTEGRATION_PACKAGE},${PACKAGES})

root-test: ## run tests, except integration tests
	@echo "$@"
	@go test ${TESTFLAGS} ${SNAPSHOT_PACKAGES} -test.root

integration: ## run integration tests
	@echo "$@"
	@go test ${TESTFLAGS} ${INTEGRATION_PACKAGE}

FORCE:

# Build a Windows binary from a cmd.
bin/%.exe: cmd/% FORCE
	@test "github.com/containerd/containerd" = "${PKG}" || \
		(echo "Please correctly set up your Go build environment. This project must be located at <GOPATH>/src/${PKG}" && false)
	@echo "$@ (Windows)"
	@if [ $$(go env GOHOSTOS) != 'windows' ]; then export GOOS="windows"; fi; \
		go build -i -o $@ ${GO_LDFLAGS}  ${GO_GCFLAGS} ./$<

# Build a Unix binary from a cmd.
bin/%: cmd/% FORCE
	@test $$(go list) = "${PKG}" || \
		(echo "Please correctly set up your Go build environment. This project must be located at <GOPATH>/src/${PKG}" && false)
	@echo "$@ (Unix)"
	@if [ $$(go env GOHOSTOS) = 'windows' ]; then export GOOS="linux"; fi; \
	 go build -i -o $@ ${GO_LDFLAGS}  ${GO_GCFLAGS} ./$<

unixbinaries: $(UNIXBINARIES) ## build Unix binaries
	@echo "$@"

windowsbinaries: $(WINDOWSBINARIES) ## build Windows binaries
	@echo "$@"

binaries: $(UNIXBINARIES) $(WINDOWSBINARIES) ## build Unix and Windows binaries
	@echo "$@"

clean: ## clean up binaries
	@echo "$@"
	@rm -f $(UNIXBINARIES)
	@rm -f $(WINDOWSBINARIES)

install: ## install binaries
	@echo "$@ $(UNIXBINARIES)"
	@mkdir -p $(DESTDIR)/bin
	@install $(UNIXBINARIES) $(DESTDIR)/bin

uninstall:
	@echo "$@"
	@rm -f $(addprefix $(DESTDIR)/bin/,$(notdir $(UNIXBINARIES)))


coverage: ## generate coverprofiles from the unit tests, except tests that require root
	@echo "$@"
	@rm -f coverage.txt
	( for pkg in $(filter-out ${INTEGRATION_PACKAGE},${PACKAGES}); do \
		go test -i ${TESTFLAGS} -test.short -coverprofile=coverage.out -covermode=atomic $$pkg || exit; \
		if [ -f profile.out ]; then \
			cat profile.out >> coverage.txt; \
			rm profile.out; \
		fi; \
		go test ${TESTFLAGS} -test.short -coverprofile=coverage.out -covermode=atomic $$pkg || exit; \
		if [ -f profile.out ]; then \
			cat profile.out >> coverage.txt; \
			rm profile.out; \
		fi; \
	done )

root-coverage: ## generae coverage profiles for the unit tests
	@echo "$@"
	@( for pkg in ${SNAPSHOT_PACKAGES}; do \
		go test -i ${TESTFLAGS} -test.short -coverprofile="../../../$$pkg/coverage.txt" -covermode=atomic $$pkg -test.root || exit; \
		go test ${TESTFLAGS} -test.short -coverprofile="../../../$$pkg/coverage.txt" -covermode=atomic $$pkg -test.root || exit; \
	done )

coverage-integration: ## generate coverprofiles from the integration tests
	@echo "$@"
	go test ${TESTFLAGS} -test.short -coverprofile="../../../${INTEGRATION_PACKAGE}/coverage.txt" -covermode=atomic ${INTEGRATION_PACKAGE}

vendor:
	@echo "$@"
	@vndr

help: ## this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort
