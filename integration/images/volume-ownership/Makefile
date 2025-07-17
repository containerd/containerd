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

PROJ=ghcr.io/containerd
VERSION=2.1
IMAGE=$(PROJ)/volume-ownership:$(VERSION)
DOCKER_CERT_PATH ?= "$(HOME)/.docker"
REMOTE_DOCKER_URL ?=
DOCKER_REMOTE_ARGS ?=


ifneq ($(REMOTE_DOCKER_URL),)
	DOCKER_REMOTE_ARGS = --tlsverify --tlscacert "$(DOCKER_CERT_PATH)/ca.pem" \
		--tlscert "$(DOCKER_CERT_PATH)/cert.pem" \
		--tlskey "$(DOCKER_CERT_PATH)/key.pem" \
		-H "$(REMOTE_DOCKER_URL)"
endif

# Operating systems supported: linux, windows
OS ?= linux
# Architectures supported: amd64, arm64
ARCH ?= amd64
# OS Version for the Windows images: 1809, 20H2, ltsc2022
OSVERSION ?= 1809

# The output type could either be docker (local), or registry.
# If it is registry, it will also allow us to push the Windows images.
OUTPUT_TYPE ?= docker

ALL_OS = linux
ALL_ARCH.linux = amd64 arm64 ppc64le
ALL_OS_ARCH.linux = $(foreach arch, ${ALL_ARCH.linux}, linux-$(arch))

ifneq ($(REMOTE_DOCKER_URL),)
ALL_OS += windows
ALL_OSVERSIONS.windows := 1809 20H2 ltsc2022
ALL_OS_ARCH.windows = $(foreach osversion, ${ALL_OSVERSIONS.windows}, windows-amd64-${osversion})
BASE.windows := mcr.microsoft.com/windows/nanoserver
endif

BASE.linux.amd64 := busybox
BASE.linux.arm64 := arm64v8/busybox
BASE.linux.ppc64le := busybox
BASE.linux := ${BASE.linux.${ARCH}}
BASE := ${BASE.${OS}}

ALL_OS_ARCH = $(foreach os, $(ALL_OS), ${ALL_OS_ARCH.${os}})

configure-docker:
	gcloud auth configure-docker --quiet
	gcloud auth configure-docker --quiet $(shell echo $(PROJ) | cut -f 1 -d "/") || true

setup-buildx:
	docker buildx use img-builder || docker buildx create --name img-builder --use

build: setup-buildx build-local

push: configure-docker setup-buildx build-registry push-manifest

build-local: $(addprefix sub-container-docker-,$(ALL_OS_ARCH.linux))
build-tools:
	GOOS=windows go build -mod=vendor -o tools/get_owner_windows.exe tools/get_owner_windows.go

clean-tools:
	rm -f tools/get_owner_windows.exe || true

build-registry: build-tools $(addprefix sub-container-registry-,$(ALL_OS_ARCH)) clean-tools

# split words on hyphen, access by 1-index
word-hyphen = $(word $2,$(subst -, ,$1))
sub-container-%:
	$(MAKE) OUTPUT_TYPE=$(call word-hyphen,$*,1) OS=$(call word-hyphen,$*,2) ARCH=$(call word-hyphen,$*,3) OSVERSION=$(call word-hyphen,$*,4) container

container: .container-${OS}-$(ARCH)

.container-linux-$(ARCH):
	docker buildx build --provenance=false --pull --output=type=${OUTPUT_TYPE} --platform ${OS}/${ARCH} \
		-t $(IMAGE)-${OS}-${ARCH} --build-arg BASE=${BASE} .

.container-windows-$(ARCH):
	docker $(DOCKER_REMOTE_ARGS) build --isolation=hyperv --no-cache --pull \
		-t $(IMAGE)-${OS}-${ARCH}-${OSVERSION} --build-arg BASE=${BASE}:${OSVERSION} \
		-f Dockerfile_windows .
	docker $(DOCKER_REMOTE_ARGS) push $(IMAGE)-${OS}-${ARCH}-${OSVERSION}

# For Windows images, we also need to include the "os.version" in the manifest list images,
# so the Windows node can pull the proper image it needs.
push-manifest:
	docker manifest create --amend $(IMAGE) $(shell echo $(ALL_OS_ARCH) | sed -e "s~[^ ]*~$(IMAGE)\-&~g")
	set -x; for arch in $(ALL_ARCH.linux); do docker manifest annotate --os linux --arch $${arch} ${IMAGE} ${IMAGE}-linux-$${arch}; done
	# we use awk to also trim the quotes around the OS version string.
	set -x; \
	for osversion in ${ALL_OSVERSIONS.windows}; do \
		full_version=`docker manifest inspect ${BASE.windows}:$${osversion} | grep "os.version" | head -n 1 | awk -F\" '{print $$4}'` || true; \
		docker manifest annotate --os windows --arch amd64 --os-version $${full_version} \
		${IMAGE} ${IMAGE}-windows-amd64-$${osversion}; \
	done
	docker manifest push --purge ${IMAGE}

.PHONY: configure-docker setup-buildx build push build-local build-registry container push-manifest clean-tools
