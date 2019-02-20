
.PHONY: clean binaries generate lint vet test
all: vet lint test binaries

binaries: bin/btrfs-test

vet:
	go vet ./...

lint:
	golint ./...

test:
	go test -v ./...

bin/%: ./cmd/% *.go
	go build -o ./$@ ./$<

clean:
	rm -rf bin/*
