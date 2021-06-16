module github.com/containerd/containerd/integration/client

go 1.15

require (
	github.com/Microsoft/hcsshim v0.8.17
	github.com/Microsoft/hcsshim/test v0.0.0-20210408205431-da33ecd607e1
	github.com/containerd/cgroups v1.0.1
	// the actual version of containerd is replaced with the code at the root of this repository
	github.com/containerd/containerd v1.5.1
	github.com/containerd/go-runc v1.0.0
	github.com/containerd/ttrpc v1.0.2
	github.com/containerd/typeurl v1.0.2
	github.com/gogo/protobuf v1.3.2
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.0.1
	github.com/opencontainers/runtime-spec v1.0.3-0.20210326190908-1c3f411f0417
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.7.0
	golang.org/x/sys v0.0.0-20210426230700-d19ff857e887
	gotest.tools/v3 v3.0.3
)

replace (
	// use the containerd module from this repository instead of downloading
	//
	// IMPORTANT: this replace rule ONLY replaces containerd itself; dependencies
	// in the "require" section above are still taken into account for version
	// resolution if newer.
	github.com/containerd/containerd => ../../

	// Replace rules below must be kept in sync with the main go.mod file at the
	// root, because that's the actual version expected by the "containerd/containerd"
	// dependency above.
	github.com/gogo/googleapis => github.com/gogo/googleapis v1.3.2
	github.com/golang/protobuf => github.com/golang/protobuf v1.3.5
	// urfave/cli must be <= v1.22.1 due to a regression: https://github.com/urfave/cli/issues/1092
	github.com/urfave/cli => github.com/urfave/cli v1.22.1
	google.golang.org/genproto => google.golang.org/genproto v0.0.0-20200224152610-e50cd9704f63
	google.golang.org/grpc => google.golang.org/grpc v1.27.1
)
