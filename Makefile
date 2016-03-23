BUILDTAGS=

GIT_COMMIT := $(shell git rev-parse HEAD 2> /dev/null || true)
GIT_BRANCH := $(shell git rev-parse --abbrev-ref HEAD 2> /dev/null)

LDFLAGS := -X github.com/docker/containerd.GitCommit=${GIT_COMMIT} ${LDFLAGS}

# if this session isn't interactive, then we don't want to allocate a
# TTY, which would fail, but if it is interactive, we do want to attach
# so that the user can send e.g. ^C through.
INTERACTIVE := $(shell [ -t 0 ] && echo 1 || echo 0)
ifeq ($(INTERACTIVE), 1)
	DOCKER_FLAGS += -t
endif

DOCKER_ENVS := -e BUILDFLAGS \
	-e LDFLAGS

# to allow `make BIND_DIR=. shell` or `make BIND_DIR= test`
# (default to no bind mount if DOCKER_HOST is set)
# note: BINDDIR is supported for backwards-compatibility here
BIND_DIR := $(if $(BIND_DIR),$(BIND_DIR),bin)
DOCKER_MOUNT := $(if $(BIND_DIR),-v "$(CURDIR)/$(BIND_DIR):/go/src/github.com/docker/containerd/$(BIND_DIR)")

DOCKER_IMAGE := containerd-dev$(if $(GIT_BRANCH),:$(GIT_BRANCH))
DOCKER_RUN := docker run --rm -i $(DOCKER_FLAGS) $(DOCKER_ENVS) $(DOCKER_MOUNT) "$(DOCKER_IMAGE)"

default: binary
static: client-static daemon-static shim-static

all: build
	$(DOCKER_RUN) hack/make.sh

binary: build
	$(DOCKER_RUN) hack/make.sh ctr containerd containerd-shim

static: client-static daemon-static shim-static

bin:
	mkdir -p bin/

client: build
	$(DOCKER_RUN) hack/make.sh ctr

daemon: build
	$(DOCKER_RUN) hack/make.sh containerd

shim: build
	$(DOCKER_RUN) hack/make.sh containerd-shim

client-static: build
	LDFLAGS="$(LDFLAGS) -w -extldflags -static" $(DOCKER_RUN) hack/make.sh ctr

daemon-static: build
	LDFLAGS="$(LDFLAGS) -w -extldflags -static" $(DOCKER_RUN) hack/make.sh containerd

shim-static: build
	LDFLAGS="$(LDFLAGS) -w -extldflags -static" $(DOCKER_RUN) hack/make.sh containerd-shim

build: bin
	@docker build -t "$(DOCKER_IMAGE)" .

validate: build
	$(DOCKER_RUN) hack/make.sh validate

vet: build
	$(DOCKER_RUN) hack/make.sh vet

test: build
	$(DOCKER_RUN) hack/make.sh test

lint: build
	$(DOCKER_RUN) hack/make.sh lint

install:
	cp bin/* /usr/local/bin/
