module github.com/containerd/containerd/cmd

go 1.16

replace (
	github.com/containerd/containerd => ../
	github.com/containerd/containerd/pkg/cri => ../pkg/cri
)

require (
	github.com/Microsoft/go-winio v0.5.2
	github.com/Microsoft/hcsshim v0.9.2
	github.com/containerd/aufs v1.0.0
	github.com/containerd/cgroups v1.0.3
	github.com/containerd/console v1.0.3
	github.com/containerd/containerd v1.6.1
	github.com/containerd/containerd/pkg/cri v0.0.0-00010101000000-000000000000
	github.com/containerd/fifo v1.0.0
	github.com/containerd/go-cni v1.1.4
	github.com/containerd/go-runc v1.0.0
	github.com/containerd/ttrpc v1.1.0
	github.com/containerd/typeurl v1.0.3-0.20220324183432-6193a0e03259
	github.com/containerd/zfs v1.0.0
	github.com/coreos/go-systemd/v22 v22.3.2
	github.com/docker/go-metrics v0.0.1
	github.com/docker/go-units v0.4.0
	github.com/gogo/protobuf v1.3.2
	github.com/moby/sys/signal v0.7.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.0.3-0.20220303224323-02efb9a75ee1
	github.com/opencontainers/runtime-spec v1.0.3-0.20210326190908-1c3f411f0417
	github.com/pelletier/go-toml v1.9.4
	github.com/sirupsen/logrus v1.8.1
	github.com/urfave/cli v1.22.5
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	golang.org/x/sys v0.0.0-20220406163625-3f8b81556e12
	google.golang.org/grpc v1.45.0
	google.golang.org/protobuf v1.28.0
)
