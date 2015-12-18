BUILDTAGS=libcontainer

# if this session isn't interactive, then we don't want to allocate a
# TTY, which would fail, but if it is interactive, we do want to attach
# so that the user can send e.g. ^C through.
INTERACTIVE := $(shell [ -t 0 ] && echo 1 || echo 0)
ifeq ($(INTERACTIVE), 1)
	DOCKER_FLAGS += -t
endif

DOCKER_IMAGE := containerd-dev$(if $(GIT_BRANCH),:$(GIT_BRANCH))
DOCKER_RUN := docker run --rm -i $(DOCKER_FLAGS) "$(DOCKER_IMAGE)"


all: client daemon

bin:
	mkdir -p bin/

clean:
	rm -rf bin

client: bin
	cd ctr && go build -o ../bin/ctr

daemon: bin
	cd containerd && go build -tags "$(BUILDTAGS)" -o ../bin/containerd

dbuild:
	@docker build --rm --force-rm -t "$(DOCKER_IMAGE)" .

dtest: dbuild
	$(DOCKER_RUN) make test

install:
	cp bin/* /usr/local/bin/

# to compile without installing protoc:
#   docker pull quay.io/pedge/protoeasy
#   docker run -d -p 6789:6789 quay.io/pedge/protoeasy
#   export PROTOEASY_ADDRESS=0.0.0.0:6789 # or whatever your docker host address is

protoc:
	go get -v go.pedge.io/protoeasy/cmd/protoeasy
	protoeasy --go --go-import-path github.com/docker/containerd --grpc .

fmt:
	@gofmt -s -l . | grep -v vendor | grep -v .pb. | tee /dev/stderr

lint:
	@golint ./... | grep -v vendor | grep -v .pb. | tee /dev/stderr

shell:
	$(DOCKER_RUN) bash

test: all validate
	go test -v $(shell go list ./... | grep -v /vendor)

validate: fmt lint vet

vet:
	go vet $(shell go list ./... | grep -v vendor)
