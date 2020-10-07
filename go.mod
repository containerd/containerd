module github.com/containerd/containerd

go 1.15

require (
	github.com/BurntSushi/toml v0.3.1
	github.com/Microsoft/go-winio v0.4.15-0.20200908182639-5b44b70ab3ab
	github.com/Microsoft/hcsshim v0.8.10
	github.com/Microsoft/hcsshim/test v0.0.0-20201001234239-936eeeb286fd
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cilium/ebpf v0.0.0-20200702112145-1c8d4c9ef775 // indirect
	github.com/containerd/aufs v0.0.0-20200908144142-dab0cbea06f4
	github.com/containerd/btrfs v0.0.0-20200117014249-153935315f4a
	github.com/containerd/cgroups v0.0.0-20200824123100-0b889c03f102
	github.com/containerd/console v1.0.1
	github.com/containerd/continuity v0.0.0-20200710164510-efbc4488d8fe
	github.com/containerd/fifo v0.0.0-20200410184934-f15a3290365b
	github.com/containerd/go-cni v1.0.1
	github.com/containerd/go-runc v0.0.0-20200220073739-7016d3ce2328
	github.com/containerd/imgcrypt v1.0.1
	github.com/containerd/nri v0.0.0-20201007170849-eb1350a75164
	github.com/containerd/ttrpc v1.0.2
	github.com/containerd/typeurl v1.0.1
	github.com/containerd/zfs v0.0.0-20200918131355-0a33824f23a2
	github.com/containernetworking/cni v0.8.0 // indirect
	github.com/containernetworking/plugins v0.8.6
	github.com/containers/ocicrypt v1.0.1 // indirect
	github.com/coreos/go-systemd/v22 v22.1.0
	github.com/davecgh/go-spew v1.1.1
	github.com/docker/docker v17.12.0-ce-rc1.0.20200310163718-4634ce647cf2+incompatible
	github.com/docker/go-events v0.0.0-20190806004212-e31b211e4f1c
	github.com/docker/go-metrics v0.0.1
	github.com/docker/go-units v0.4.0
	github.com/docker/spdystream v0.0.0-20160310174837-449fdfce4d96 // indirect
	github.com/emicklei/go-restful v2.9.5+incompatible
	github.com/fsnotify/fsnotify v1.4.9
	github.com/go-logr/logr v0.2.0 // indirect
	github.com/gogo/googleapis v1.4.0
	github.com/gogo/protobuf v1.3.1
	github.com/golang/protobuf v1.4.2
	github.com/google/go-cmp v0.5.1
	github.com/google/gofuzz v1.1.0 // indirect
	github.com/google/uuid v1.1.1
	github.com/grpc-ecosystem/go-grpc-prometheus v1.2.0
	github.com/hashicorp/errwrap v1.0.0 // indirect
	github.com/hashicorp/go-multierror v1.0.0
	github.com/hashicorp/golang-lru v0.5.3 // indirect
	github.com/imdario/mergo v0.3.10
	github.com/json-iterator/go v1.1.10 // indirect
	github.com/mistifyio/go-zfs v2.1.2-0.20190413222219-f784269be439+incompatible // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.1 // indirect
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.0.1
	github.com/opencontainers/runc v1.0.0-rc92
	github.com/opencontainers/runtime-spec v1.0.3-0.20200728170252-4d89ac9fbff6
	github.com/opencontainers/selinux v1.6.0
	github.com/pkg/errors v0.9.1
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/client_golang v1.7.1
	github.com/prometheus/client_model v0.2.0 // indirect
	github.com/shurcooL/sanitized_anchor_name v1.0.0 // indirect
	github.com/sirupsen/logrus v1.7.0
	github.com/stretchr/testify v1.4.0
	github.com/syndtr/gocapability v0.0.0-20180916011248-d98352740cb2
	github.com/tchap/go-patricia v2.2.6+incompatible // indirect
	github.com/urfave/cli v1.22.2
	github.com/willf/bitset v1.1.11 // indirect
	go.etcd.io/bbolt v1.3.5
	golang.org/x/crypto v0.0.0-20200622213623-75b288015ac9 // indirect
	golang.org/x/net v0.0.0-20200707034311-ab3426394381
	golang.org/x/oauth2 v0.0.0-20191202225959-858c2ad4c8b6 // indirect
	golang.org/x/sync v0.0.0-20200625203802-6e8e738ad208
	golang.org/x/sys v0.0.0-20200916030750-2334cc1a136f
	golang.org/x/text v0.3.3 // indirect
	golang.org/x/time v0.0.0-20191024005414-555d28b269f0 // indirect
	google.golang.org/grpc v1.30.0
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/square/go-jose.v2 v2.3.1 // indirect
	gotest.tools/v3 v3.0.2
	k8s.io/api v0.19.2
	k8s.io/apimachinery v0.19.2
	k8s.io/apiserver v0.19.0-rc.4
	k8s.io/client-go v0.19.2
	k8s.io/component-base v0.19.2
	k8s.io/cri-api v0.19.0-rc.4
	k8s.io/klog/v2 v2.2.0
	k8s.io/utils v0.0.0-20200729134348-d5654de09c73
	sigs.k8s.io/yaml v1.2.0 // indirect
)
