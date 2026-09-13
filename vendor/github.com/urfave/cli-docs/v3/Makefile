.PHONY: all
all: vet test show-cover

.PHONY: vet
vet:
	go vet -v ./...

.PHONY: test
test:
	go test -v -cover -coverprofile=coverage.txt ./...

.PHONY: show-cover
show-cover:
	go tool cover -func=coverage.txt
