gentest:
	rm -rf ./testing/cbor_gen.go ./testing/cbor_map_gen.go
	go run ./testgen/main.go
.PHONY: gentest

test: gentest
	go test ./...
.PHONY: test
