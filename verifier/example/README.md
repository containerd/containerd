# Example Verifier Plugin

This package implements an example verifier plugin with a simple image digest allowlist. To test the example, run the following commands in separate terminals from the root of this repository:

```
# Terminal 1
make bin/containerd && sudo ./bin/containerd -c ./verifier/example/containerd.toml

# Terminal 2
sudo go run ./verifier/example/main.go /run/example-verifier.sock sha256:ff6bdca1701f3a8a67e328815ff2346b0e4067d32ec36b7992c1fdc001dc8517 sha256:aa0afebbb3cfa473099a62c4b32e9b3fb73ed23f2a75a65ce1d4b4f55a5c2ef2

# Terminal 3
make bin/ctr
# Image is allowed:
sudo ./bin/ctr image pull --local=false index.docker.io/library/alpine@sha256:ff6bdca1701f3a8a67e328815ff2346b0e4067d32ec36b7992c1fdc001dc8511
# Image is not allowed:
sudo ./bin/ctr image pull --local=false index.docker.io/library/alpine@sha256:c75ac27b49326926b803b9ed43bf088bc220d22556de1bc5f72d742c91398f69

