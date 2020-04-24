module github.com/containerd/cri

go 1.13

replace (
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
	github.com/cilium/ebpf v0.0.0-20191203103619-60c3aa43f488 // indirect
	github.com/containerd/cgroups v0.0.0-20200113070643-7347743e5d1e
	github.com/containerd/console v1.0.0 // indirect
	github.com/containerd/containerd v1.3.1-0.20200305210454-01310155947c
	github.com/containerd/continuity v0.0.0-20200107062522-0ec596719c75
	github.com/containerd/fifo v0.0.0-20190816180239-bda0ff6ed73c
	github.com/containerd/go-cni v0.0.0-20190822145629-0d360c50b10b
	github.com/containerd/go-runc v0.0.0-20191206163734-a5c2862aed5e // indirect
	github.com/containerd/imgcrypt v1.0.1
	github.com/containerd/ttrpc v1.0.0 // indirect
	github.com/containerd/typeurl v1.0.0
	github.com/containernetworking/plugins v0.7.6
	github.com/davecgh/go-spew v1.1.1
	github.com/docker/distribution v2.7.1+incompatible
	github.com/docker/docker v1.4.2-0.20200310163718-4634ce647cf2
	github.com/docker/go-metrics v0.0.1 // indirect
	github.com/fsnotify/fsnotify v1.4.8
	github.com/gogo/googleapis v1.3.2 // indirect
	github.com/gogo/protobuf v1.3.1
	github.com/golang/protobuf v1.3.3
	github.com/hashicorp/golang-lru v0.5.3 // indirect
	github.com/opencontainers/go-digest v1.0.0-rc1.0.20180430190053-c9281466c8b2
	github.com/opencontainers/image-spec v1.0.1
	github.com/opencontainers/runc v1.0.0-rc8.0.20190926000215-3e425f80a8c9
	github.com/opencontainers/runtime-spec v1.0.2-0.20190207185410-29686dbc5559
	github.com/opencontainers/selinux v1.5.1
	github.com/pkg/errors v0.8.1
	github.com/prometheus/client_golang v1.3.0 // indirect
	github.com/sirupsen/logrus v1.4.2
	github.com/stretchr/testify v1.4.0
	github.com/tchap/go-patricia v2.2.6+incompatible // indirect
	golang.org/x/net v0.0.0-20191004110552-13f9640d40b9
	golang.org/x/sys v0.0.0-20200302150141-5c8b2ff67527
	google.golang.org/genproto v0.0.0-20200224152610-e50cd9704f63 // indirect
	google.golang.org/grpc v1.27.1
	gotest.tools/v3 v3.0.2 // indirect
	k8s.io/apimachinery v0.18.2-beta.0
	k8s.io/client-go v0.18.0
	k8s.io/cri-api v0.18.3-beta.0
	k8s.io/klog v1.0.0
	k8s.io/kubernetes v1.18.0
	k8s.io/utils v0.0.0-20200324210504-a9aa75ae1b89
)
