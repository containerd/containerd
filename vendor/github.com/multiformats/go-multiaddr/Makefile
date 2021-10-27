conformance: tmp/multiaddr
	go build -o tmp/multiaddr/test/go-multiaddr ./multiaddr
	cd tmp/multiaddr/test && MULTIADDR_BIN="./go-multiaddr" go test -v

tmp/multiaddr:
	mkdir -p tmp/
	git clone https://github.com/multiformats/multiaddr tmp/multiaddr/

clean:
	rm -rf tmp/

.PHONY: conformance clean
