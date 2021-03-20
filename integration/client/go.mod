module github.com/containerd/containerd/integration/client

go 1.15

require (
	github.com/Microsoft/hcsshim v0.8.15
	github.com/Microsoft/hcsshim/test v0.0.0-20210227013316-43a75bb4edd3
	github.com/containerd/cgroups v0.0.0-20210114181951-8a68de567b68
	github.com/containerd/containerd v1.5.0-beta.3
	github.com/containerd/go-runc v0.0.0-20201020171139-16b287bc67d0
	github.com/containerd/ttrpc v1.0.2
	github.com/containerd/typeurl v1.0.1
	github.com/gogo/protobuf v1.3.2
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.0.1
	github.com/opencontainers/runtime-spec v1.0.3-0.20200929063507-e6143ca7d51d
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.8.0
	golang.org/x/sys v0.0.0-20210124154548-22da62e12c0c
	gotest.tools/v3 v3.0.3
)

replace github.com/containerd/containerd => ../../
