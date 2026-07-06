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

all: build

PROJ ?= ghcr.io/containerd
NAME ?= whiteout-test
VERSION ?= 1.0
IMAGE ?= $(PROJ)/$(NAME):$(VERSION)

DOCKER ?= docker
MIN_DOCKER_MAJOR ?= 29
DOCKER_CLIENT_VERSION = $(shell $(DOCKER) version --format '{{.Client.Version}}' 2>/dev/null)
DOCKER_CLIENT_MAJOR = $(firstword $(subst ., ,$(DOCKER_CLIENT_VERSION)))

# Use the same helper image as https://github.com/containerd/containerd/blob/main/test/init-buildx.sh#L78
BINFMT_IMAGE ?= multiarch/qemu-user-static@sha256:c772ee1965aa0be9915ee1b018a0dd92ea361b4fa1bcab5bbc033517749b2af4
PLATFORMS ?= linux/386,linux/amd64,linux/arm/v6,linux/arm/v7,linux/arm64/v8,linux/ppc64le,linux/riscv64,linux/s390x

# Optional/manual: register binfmt handlers for cross-arch emulation.
setup-binfmt:
	$(DOCKER) run --privileged --rm $(BINFMT_IMAGE) --reset -p yes

check-docker-version:
	@if [ -z "$(DOCKER_CLIENT_VERSION)" ]; then \
		echo "error: failed to detect Docker client version"; \
		exit 1; \
	elif ! [ "$(DOCKER_CLIENT_MAJOR)" -eq "$(DOCKER_CLIENT_MAJOR)" ] 2>/dev/null; then \
		echo "error: failed to parse Docker major version from $(DOCKER_CLIENT_VERSION)"; \
		exit 1; \
	elif [ "$(DOCKER_CLIENT_MAJOR)" -lt "$(MIN_DOCKER_MAJOR)" ]; then \
		echo "error: this Makefile requires Docker >= $(MIN_DOCKER_MAJOR); found $(DOCKER_CLIENT_VERSION)"; \
		exit 1; \
	fi

build: check-docker-version
	$(DOCKER) buildx build --platform $(PLATFORMS) -t $(IMAGE) .

push: build
	$(DOCKER) push $(IMAGE)

print-config:
	@echo "DOCKER=$(DOCKER)"
	@echo "MIN_DOCKER_MAJOR=$(MIN_DOCKER_MAJOR)"
	@echo "DOCKER_CLIENT_VERSION=$(DOCKER_CLIENT_VERSION)"
	@echo "IMAGE=$(IMAGE)"
	@echo "PLATFORMS=$(PLATFORMS)"

.PHONY: all setup-binfmt check-docker-version build push print-config
