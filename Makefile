
BUILDTAGS=libcontainer

all: mkbin client daemon

mkbin:
	mkdir -p bin/

client: mkbin
	cd cmd/ctr && go build -i -o ../bin/ctr

daemon: mkbin
	cd cmd/containerd && go build -i -tags "$(BUILDTAGS)" -o ../bin/containerd

install:
	cp bin/* /usr/local/bin/

protoc:
	protoc -I ./api/grpc/types ./api/grpc/types/api.proto --go_out=plugins=grpc:api/grpc/types
