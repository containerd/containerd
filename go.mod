module github.com/containerd/containerd

go 1.16

require (
	cloud.google.com/go v0.81.0 // indirect
	github.com/AdaLogics/go-fuzz-headers v0.0.0-20210715213245-6c3934b029d8
	github.com/Microsoft/go-winio v0.5.1
	github.com/Microsoft/hcsshim v0.9.2
	github.com/containerd/aufs v1.0.0
	github.com/containerd/btrfs v1.0.0
	github.com/containerd/cgroups v1.0.3
	github.com/containerd/console v1.0.3
	github.com/containerd/continuity v0.2.2
	github.com/containerd/fifo v1.0.0
	github.com/containerd/go-cni v1.1.3
	github.com/containerd/go-runc v1.0.0
	github.com/containerd/imgcrypt v1.1.3
	github.com/containerd/nri v0.1.0
	github.com/containerd/ttrpc v1.1.0
	github.com/containerd/typeurl v1.0.2
	github.com/containerd/zfs v1.0.0
	github.com/containernetworking/plugins v1.0.1
	github.com/coreos/go-systemd/v22 v22.3.2
	github.com/davecgh/go-spew v1.1.1
	github.com/docker/go-events v0.0.0-20190806004212-e31b211e4f1c
	github.com/docker/go-metrics v0.0.1
	github.com/docker/go-units v0.4.0
	github.com/emicklei/go-restful/v3 v3.7.3
	github.com/fsnotify/fsnotify v1.5.1
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/gogo/googleapis v1.4.0
	github.com/gogo/protobuf v1.3.2
	github.com/google/go-cmp v0.5.6
	github.com/google/uuid v1.2.0
	github.com/grpc-ecosystem/go-grpc-middleware v1.3.0
	github.com/grpc-ecosystem/go-grpc-prometheus v1.2.0
	github.com/hashicorp/go-multierror v1.1.1
	github.com/imdario/mergo v0.3.12
	github.com/intel/goresctrl v0.2.0
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.11.13
	github.com/moby/locker v1.0.1
	github.com/moby/sys/mountinfo v0.6.0
	github.com/moby/sys/signal v0.7.0
	github.com/moby/sys/symlink v0.2.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.0.2-0.20211117181255-693428a734f5
	github.com/opencontainers/runc v1.1.0
	github.com/opencontainers/runtime-spec v1.0.3-0.20210326190908-1c3f411f0417
	github.com/opencontainers/selinux v1.10.0
	github.com/pelletier/go-toml v1.9.3
	github.com/prometheus/client_golang v1.11.0
	github.com/sirupsen/logrus v1.8.1
	github.com/stretchr/testify v1.7.0
	github.com/tchap/go-patricia/v2 v2.3.1
	github.com/urfave/cli/v2 v2.3.0
	github.com/vishvananda/netlink v1.1.1-0.20210330154013-f5de75959ad5
	go.etcd.io/bbolt v1.3.6
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.28.0
	go.opentelemetry.io/otel v1.3.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.3.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.3.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.3.0
	go.opentelemetry.io/otel/sdk v1.3.0
	go.opentelemetry.io/otel/trace v1.3.0
	golang.org/x/crypto v0.0.0-20210817164053-32db794688a5 // indirect
	golang.org/x/net v0.0.0-20211216030914-fe4d6282115f // indirect
	golang.org/x/oauth2 v0.0.0-20210819190943-2bc19b11175f // indirect
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	golang.org/x/sys v0.0.0-20211216021012-1d35b9e2eb4e
	golang.org/x/term v0.0.0-20210615171337-6886f2dfbf5b // indirect
	google.golang.org/genproto v0.0.0-20211208223120-3a66f561d7aa // indirect
	google.golang.org/grpc v1.43.0
	google.golang.org/protobuf v1.27.1
	gotest.tools/v3 v3.0.3
	k8s.io/api v0.22.5
	k8s.io/apimachinery v0.22.5
	k8s.io/apiserver v0.22.5
	k8s.io/client-go v0.22.5
	k8s.io/component-base v0.22.5
	k8s.io/cri-api v0.23.1
	k8s.io/klog/v2 v2.30.0
	k8s.io/utils v0.0.0-20210930125809-cb0fa318a74b
)

// When updating replace rules, make sure to also update the rules in integration/client/go.mod and api/go.mod
replace (
	// prevent transitional dependencies due to containerd having a circular
	// dependency on itself through plugins. see .empty-mod/go.mod for details
	github.com/containerd/containerd => ./.empty-mod/
	github.com/gogo/googleapis => github.com/gogo/googleapis v1.3.2
	google.golang.org/genproto => google.golang.org/genproto v0.0.0-20200224152610-e50cd9704f63
)
