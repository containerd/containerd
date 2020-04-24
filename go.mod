module github.com/containerd/containerd

go 1.13

replace (
	github.com/containerd/cri => github.com/ibuildthecloud/cri v1.11.1-0.20200424181029-98ce69fcbde3
	k8s.io/api v0.0.0 => k8s.io/api v0.18.0
	k8s.io/apiextensions-apiserver v0.0.0 => k8s.io/apiextensions-apiserver v0.18.0
	k8s.io/apimachinery v0.0.0 => k8s.io/apimachinery v0.18.0
	k8s.io/apiserver v0.0.0 => k8s.io/apiserver v0.18.0
	k8s.io/cli-runtime v0.0.0 => k8s.io/cli-runtime v0.18.0
	k8s.io/client-go v0.0.0 => k8s.io/client-go v0.18.0
	k8s.io/cloud-provider v0.0.0 => k8s.io/cloud-provider v0.18.0
	k8s.io/cluster-bootstrap v0.0.0 => k8s.io/cluster-bootstrap v0.18.0
	k8s.io/code-generator v0.0.0 => k8s.io/code-generator v0.18.0
	k8s.io/component-base v0.0.0 => k8s.io/component-base v0.18.0
	k8s.io/cri-api v0.0.0 => k8s.io/cri-api v0.18.0
	k8s.io/csi-translation-lib v0.0.0 => k8s.io/csi-translation-lib v0.18.0
	k8s.io/kube-aggregator v0.0.0 => k8s.io/kube-aggregator v0.18.0
	k8s.io/kube-controller-manager v0.0.0 => k8s.io/kube-controller-manager v0.18.0
	k8s.io/kube-proxy v0.0.0 => k8s.io/kube-proxy v0.18.0
	k8s.io/kube-scheduler v0.0.0 => k8s.io/kube-scheduler v0.18.0
	k8s.io/kubectl v0.0.0 => k8s.io/kubectl v0.18.0
	k8s.io/kubelet v0.0.0 => k8s.io/kubelet v0.18.0
	k8s.io/kubernetes v0.0.0 => github.com/rancher/kubernetes v0.18.0
	k8s.io/legacy-cloud-providers v0.0.0 => k8s.io/legacy-cloud-providers v0.18.0
	k8s.io/metrics v0.0.0 => k8s.io/metrics v0.18.0
	k8s.io/node-api v0.0.0 => k8s.io/node-api v0.18.0
	k8s.io/sample-apiserver v0.0.0 => k8s.io/sample-apiserver v0.18.0
	k8s.io/sample-cli-plugin v0.0.0 => k8s.io/sample-cli-plugin v0.18.0
	k8s.io/sample-controller v0.0.0 => k8s.io/sample-controller v0.18.0
)

require (
	github.com/BurntSushi/toml v0.3.1
	github.com/Microsoft/go-winio v0.4.15-0.20190919025122-fc70bd9a86b5
	github.com/Microsoft/hcsshim v0.8.8-0.20200109000640-0b571ac85d7c
	github.com/containerd/aufs v0.0.0-20191030083217-371312c1e31c
	github.com/containerd/btrfs v0.0.0-20200117014249-153935315f4a
	github.com/containerd/cgroups v0.0.0-20200327175542-b44481373989
	github.com/containerd/console v1.0.0
	github.com/containerd/continuity v0.0.0-20200107062522-0ec596719c75
	github.com/containerd/cri v1.11.1-0.20200416235649-61b7af756460
	github.com/containerd/fifo v0.0.0-20190816180239-bda0ff6ed73c
	github.com/containerd/go-runc v0.0.0-20191206163734-a5c2862aed5e
	github.com/containerd/ttrpc v1.0.1-0.20191021111801-6e416eafd26e
	github.com/containerd/typeurl v1.0.0
	github.com/containerd/zfs v0.0.0-20191030014035-9abf673ca6ff
	github.com/coreos/go-systemd/v22 v22.0.0
	github.com/docker/go-events v0.0.0-20190806004212-e31b211e4f1c
	github.com/docker/go-metrics v0.0.1
	github.com/docker/go-units v0.4.0
	github.com/gogo/googleapis v1.3.2
	github.com/gogo/protobuf v1.3.1
	github.com/google/go-cmp v0.3.1
	github.com/google/uuid v1.1.1
	github.com/grpc-ecosystem/go-grpc-prometheus v1.2.0
	github.com/hashicorp/go-multierror v1.0.0
	github.com/imdario/mergo v0.3.8
	github.com/mistifyio/go-zfs v2.1.2-0.20190413222219-f784269be439+incompatible // indirect
	github.com/opencontainers/go-digest v1.0.0-rc1.0.20180430190053-c9281466c8b2
	github.com/opencontainers/image-spec v1.0.1
	github.com/opencontainers/runc v1.0.0-rc8.0.20190926000215-3e425f80a8c9
	github.com/opencontainers/runtime-spec v1.0.2
	github.com/opencontainers/selinux v1.5.1
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.3.0
	github.com/sirupsen/logrus v1.4.2
	github.com/syndtr/gocapability v0.0.0-20180916011248-d98352740cb2
	github.com/urfave/cli v1.22.2
	go.etcd.io/bbolt v1.3.3
	golang.org/x/net v0.0.0-20191004110552-13f9640d40b9
	golang.org/x/sync v0.0.0-20190911185100-cd5d95a43a6e
	golang.org/x/sys v0.0.0-20200302150141-5c8b2ff67527
	google.golang.org/grpc v1.27.1
	gotest.tools/v3 v3.0.2
)
