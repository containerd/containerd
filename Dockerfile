FROM debian:jessie

RUN apt-get update && apt-get install -y \
	build-essential \
	ca-certificates \
	curl \
	git \
	make \
	cmake \
	zlib1g-dev \
	--no-install-recommends \
	&& rm -rf /var/lib/apt/lists/*

# Install Go
ENV GO_VERSION 1.5.3
RUN curl -sSL  "https://storage.googleapis.com/golang/go${GO_VERSION}.linux-amd64.tar.gz" | tar -v -C /usr/local -xz
ENV PATH /go/bin:/usr/local/go/bin:$PATH
ENV GOPATH /go:/go/src/github.com/docker/containerd/vendor

# Install golang/protobuf
RUN go get github.com/golang/protobuf/proto \
	&& go get github.com/golang/protobuf/protoc-gen-go

# Install Protobuf
ENV PROTOBUF_VERSION 3.0.0-beta-2
RUN git clone https://github.com/google/protobuf.git /usr/local/protobuf \
	&& (cd /usr/local/protobuf && git checkout -q v${PROTOBUF_VERSION} \
	&& cd cmake \
	&& cmake -Dprotobuf_BUILD_TESTS=off . \
	&& make \
	&& make install)
RUN rm -rf /usr/local/protobuf

# install golint/vet
RUN go get github.com/golang/lint/golint \
	&& go get golang.org/x/tools/cmd/vet

WORKDIR /go/src/github.com/docker/containerd

COPY . /go/src/github.com/docker/containerd

