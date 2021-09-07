module github.com/Microsoft/hcsshim/test

go 1.16

require (
	github.com/Microsoft/go-winio v0.4.17
	github.com/Microsoft/hcsshim v0.8.16
	github.com/containerd/containerd v1.5.1
	github.com/containerd/go-runc v1.0.0
	github.com/containerd/ttrpc v1.0.2
	github.com/containerd/typeurl v1.0.2
	github.com/gogo/protobuf v1.3.2
	github.com/opencontainers/runtime-spec v1.0.3-0.20200929063507-e6143ca7d51d
	github.com/opencontainers/runtime-tools v0.0.0-20181011054405-1d69bd0f9c39
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.7.0
	golang.org/x/sync v0.0.0-20201207232520-09787c993a3a
	golang.org/x/sys v0.0.0-20210324051608-47abb6519492
	google.golang.org/grpc v1.33.2
	k8s.io/cri-api v0.20.6
)

replace (
	github.com/Microsoft/hcsshim => ../
	google.golang.org/genproto => google.golang.org/genproto v0.0.0-20200224152610-e50cd9704f63
	google.golang.org/grpc => google.golang.org/grpc v1.27.1
)
