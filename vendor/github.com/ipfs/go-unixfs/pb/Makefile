PB = $(wildcard *.proto)
GO = $(PB:.proto=.pb.go)

all: $(GO)

%.pb.go: %.proto
		protoc --proto_path=$(GOPATH)/src:. --gogo_out=. $<

clean:
		rm -f *.pb.go
		rm -f *.go
