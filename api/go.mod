module github.com/containerd/containerd/api

go 1.16

require (
	github.com/containerd/ttrpc v1.0.2
	github.com/containerd/typeurl v1.0.2
	github.com/gogo/googleapis v1.4.0
	github.com/gogo/protobuf v1.3.2
	github.com/opencontainers/go-digest v1.0.0
	google.golang.org/grpc v1.38.0
)

replace (
	github.com/gogo/googleapis => github.com/gogo/googleapis v1.3.2
	google.golang.org/genproto => google.golang.org/genproto v0.0.0-20200224152610-e50cd9704f63
)
