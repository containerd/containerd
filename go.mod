module github.com/containerd/containerd

go 1.12

require (
	github.com/BurntSushi/toml v0.3.1
	github.com/Microsoft/go-winio v0.0.0-20190408173621-84b4ab48a507
	github.com/Microsoft/hcsshim v0.0.0-20190325164909-8abdbb8205e4
	github.com/beorn7/perks v0.0.0-20160804104726-4c0e84591b9a // indirect
	github.com/containerd/aufs v0.0.0-20190114185352-f894a800659b
	github.com/containerd/btrfs v0.0.0-20181101203652-af5082808c83
	github.com/containerd/cgroups v0.0.0-20190328223300-4994991857f9
	github.com/containerd/console v0.0.0-20181022165439-0650fd9eeb50
	github.com/containerd/continuity v0.0.0-20181001140422-bd77b46c8352
	github.com/containerd/cri v0.0.0-20190426014833-2fc62db8146c
	github.com/containerd/fifo v0.0.0-20180307165137-3d5202aec260
	github.com/containerd/go-cni v0.0.0-20190409231530-891c2a41e181 // indirect
	github.com/containerd/go-runc v0.0.0-20180907222934-5a6d9f37cfa3
	github.com/containerd/ttrpc v0.0.0-20190529185706-a5bd8ce9e40b
	github.com/containerd/typeurl v0.0.0-20180627222232-a93fcdb778cd
	github.com/containerd/zfs v0.0.0-20181107152433-31af176f2ae8
	github.com/containernetworking/cni v0.6.0 // indirect
	github.com/containernetworking/plugins v0.7.0 // indirect
	github.com/coreos/go-systemd v0.0.0-20161114122254-48702e0da86b // indirect
	github.com/docker/distribution v0.0.0-20190205005809-0d3efadf0154
	github.com/docker/docker v0.0.0-20171019062838-86f080cff091 // indirect
	github.com/docker/go-events v0.0.0-20170721190031-9461782956ad
	github.com/docker/go-metrics v0.0.0-20180131145841-4ea375f7759c
	github.com/docker/go-units v0.4.0
	github.com/docker/spdystream v0.0.0-20160310174837-449fdfce4d96 // indirect
	github.com/elazarl/goproxy v0.0.0-20190421051319-9d40249d3c2f // indirect
	github.com/elazarl/goproxy/ext v0.0.0-20190421051319-9d40249d3c2f // indirect
	github.com/emicklei/go-restful v2.2.1+incompatible // indirect
	github.com/godbus/dbus v0.0.0-20151105175453-c7fdd8b5cd55 // indirect
	github.com/gogo/googleapis v1.2.0
	github.com/gogo/protobuf v1.2.1
	github.com/google/go-cmp v0.2.0
	github.com/google/gofuzz v0.0.0-20161122191042-44d81051d367 // indirect
	github.com/google/uuid v1.1.1 // indirect
	github.com/grpc-ecosystem/go-grpc-prometheus v0.0.0-20160910222444-6b7015e65d36
	github.com/hashicorp/errwrap v0.0.0-20141028054710-7554cd9344ce // indirect
	github.com/hashicorp/go-multierror v0.0.0-20161216184304-ed905158d874
	github.com/json-iterator/go v1.1.5 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.1 // indirect
	github.com/mistifyio/go-zfs v0.0.0-20190413222219-f784269be439 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v0.0.0-20180701023420-4b7aa43c6742 // indirect
	github.com/onsi/ginkgo v1.8.0 // indirect
	github.com/onsi/gomega v1.5.0 // indirect
	github.com/opencontainers/go-digest v0.0.0-20180430190053-c9281466c8b2
	github.com/opencontainers/image-spec v1.0.1
	github.com/opencontainers/runc v1.0.0-rc8
	github.com/opencontainers/runtime-spec v0.0.0-20190207185410-29686dbc5559
	github.com/opencontainers/selinux v1.2.2 // indirect
	github.com/pkg/errors v0.8.1
	github.com/prometheus/client_golang v0.0.0-20180131142826-f4fb1b73fb09
	github.com/prometheus/client_model v0.0.0-20171117100541-99fa1f4be8e5 // indirect
	github.com/prometheus/common v0.0.0-20180110214958-89604d197083 // indirect
	github.com/prometheus/procfs v0.0.0-20180125133057-cb4147076ac7 // indirect
	github.com/satori/go.uuid v1.2.0 // indirect
	github.com/seccomp/libseccomp-golang v0.0.0-20160531183505-32f571b70023 // indirect
	github.com/sirupsen/logrus v1.4.1
	github.com/syndtr/gocapability v0.0.0-20180916011248-d98352740cb2
	github.com/tchap/go-patricia v2.2.6+incompatible // indirect
	github.com/urfave/cli v0.0.0-20171014202726-7bc6a0acffa5
	go.etcd.io/bbolt v0.0.0-20190528202153-2eb7227adea1
	golang.org/x/crypto v0.0.0-20190411191339-88737f569e3a // indirect
	golang.org/x/net v0.0.0-20190522155817-f3200d17e092
	golang.org/x/sync v0.0.0-20181108010431-42b317875d0f
	golang.org/x/sys v0.0.0-20190602015325-4c4f7f33c9ed
	golang.org/x/time v0.0.0-20161028155119-f51c12702a4d // indirect
	google.golang.org/grpc v1.20.1
	gopkg.in/inf.v0 v0.9.0 // indirect
	gotest.tools v0.0.0-20181223230014-1083505acf35
	k8s.io/api v0.0.0-20190219093303-35c66765f3ca // indirect
	k8s.io/apimachinery v0.0.0-20190216013122-f05b8decd79c // indirect
	k8s.io/apiserver v0.0.0-20190219213931-e783488c2f43 // indirect
	k8s.io/client-go v0.0.0-20190219213553-1f401a01c752 // indirect
	k8s.io/klog v0.0.0-20181108234604-8139d8cb77af // indirect
	k8s.io/kubernetes v1.15.0-alpha.0 // indirect
	k8s.io/utils v0.0.0-20190221042446-c2654d5206da // indirect
	sigs.k8s.io/yaml v1.1.0 // indirect
)

replace github.com/golang/lint => golang.org/x/lint v0.0.0-20190409202823-959b441ac422
