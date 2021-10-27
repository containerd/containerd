pbgos := $(patsubst %.proto,%.pb.go,$(wildcard *.proto))

all: $(pbgos)

%.pb.go: %.proto
	protoc --gogofaster_out=. --proto_path=$(GOPATH)/src:. $<
