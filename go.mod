module github.com/containerd/containerd

go 1.21

require (
	dario.cat/mergo v1.0.0
	github.com/AdaLogics/go-fuzz-headers v0.0.0-20230811130428-ced1acdcaa24
	github.com/AdamKorcz/go-118-fuzz-build v0.0.0-20230306123547-8075edf89bb0
	github.com/Microsoft/go-winio v0.6.2
	github.com/Microsoft/hcsshim v0.11.7
	github.com/containerd/aufs v1.0.0
	github.com/containerd/btrfs/v2 v2.0.0
	github.com/containerd/cgroups/v3 v3.0.2
	github.com/containerd/console v1.0.3
	github.com/containerd/containerd/api v1.7.19
	github.com/containerd/continuity v0.4.2
	github.com/containerd/errdefs v0.1.0
	github.com/containerd/fifo v1.1.0
	github.com/containerd/go-cni v1.1.9
	github.com/containerd/go-runc v1.0.0
	github.com/containerd/imgcrypt v1.1.8
	github.com/containerd/log v0.1.0
	github.com/containerd/nri v0.6.1
	github.com/containerd/platforms v0.2.1
	github.com/containerd/ttrpc v1.2.5
	github.com/containerd/typeurl/v2 v2.1.1
	github.com/containerd/zfs v1.1.0
	github.com/containernetworking/cni v1.1.2
	github.com/containernetworking/plugins v1.2.0
	github.com/coreos/go-systemd/v22 v22.5.0
	github.com/davecgh/go-spew v1.1.1
	github.com/distribution/reference v0.6.0
	github.com/docker/go-events v0.0.0-20190806004212-e31b211e4f1c
	github.com/docker/go-metrics v0.0.1
	github.com/docker/go-units v0.5.0
	github.com/emicklei/go-restful/v3 v3.10.1
	github.com/fsnotify/fsnotify v1.6.0
	github.com/google/go-cmp v0.6.0
	github.com/google/uuid v1.4.0
	github.com/grpc-ecosystem/go-grpc-middleware v1.3.0
	github.com/grpc-ecosystem/go-grpc-prometheus v1.2.0
	github.com/hashicorp/go-multierror v1.1.1
	github.com/intel/goresctrl v0.3.0
	github.com/klauspost/compress v1.16.7
	github.com/minio/sha256-simd v1.0.0
	github.com/moby/locker v1.0.1
	github.com/moby/sys/mountinfo v0.6.2
	github.com/moby/sys/sequential v0.5.0
	github.com/moby/sys/signal v0.7.0
	github.com/moby/sys/symlink v0.2.0
	github.com/moby/sys/user v0.1.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.1.0
	github.com/opencontainers/runtime-spec v1.1.0
	github.com/opencontainers/runtime-tools v0.9.1-0.20221107090550-2e043c6bd626
	github.com/opencontainers/selinux v1.11.0
	github.com/pelletier/go-toml v1.9.5
	github.com/prometheus/client_golang v1.14.0
	github.com/sirupsen/logrus v1.9.3
	github.com/stretchr/testify v1.8.4
	github.com/tchap/go-patricia/v2 v2.3.1
	github.com/urfave/cli v1.22.12
	github.com/vishvananda/netlink v1.2.1-beta.2
	go.etcd.io/bbolt v1.3.10
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.46.1
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.45.0
	go.opentelemetry.io/otel v1.21.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.19.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.19.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.19.0
	go.opentelemetry.io/otel/sdk v1.21.0
	go.opentelemetry.io/otel/trace v1.21.0
	golang.org/x/sync v0.5.0
	golang.org/x/sys v0.18.0
	google.golang.org/genproto v0.0.0-20231211222908-989df2bf70f3
	google.golang.org/genproto/googleapis/rpc v0.0.0-20231212172506-995d672761c0
	google.golang.org/grpc v1.59.0
	google.golang.org/protobuf v1.33.0
	k8s.io/api v0.26.2
	k8s.io/apimachinery v0.26.2
	k8s.io/apiserver v0.26.2
	k8s.io/client-go v0.26.2
	k8s.io/component-base v0.26.2
	k8s.io/cri-api v0.27.1
	k8s.io/klog/v2 v2.90.1
	k8s.io/utils v0.0.0-20230220204549-a5ecb0141aa5
	tags.cncf.io/container-device-interface v0.7.2
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/cenkalti/backoff/v4 v4.2.1 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/cilium/ebpf v0.9.1 // indirect
	github.com/containerd/cgroups v1.1.0 // indirect
	github.com/containerd/typeurl v1.0.2 // indirect
	github.com/containers/ocicrypt v1.1.10 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.2 // indirect
	github.com/felixge/httpsnoop v1.0.3 // indirect
	github.com/go-jose/go-jose/v3 v3.0.3 // indirect
	github.com/go-logr/logr v1.3.0 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.16.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/cpuid/v2 v2.0.4 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/miekg/pkcs11 v1.1.1 // indirect
	github.com/mistifyio/go-zfs/v3 v3.0.1 // indirect
	github.com/moby/spdystream v0.2.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/client_model v0.3.0 // indirect
	github.com/prometheus/common v0.37.0 // indirect
	github.com/prometheus/procfs v0.8.0 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/stefanberger/go-pkcs11uri v0.0.0-20230803200340-78284954bff6 // indirect
	github.com/syndtr/gocapability v0.0.0-20200815063812-42c35b437635 // indirect
	github.com/vishvananda/netns v0.0.0-20210104183010-2eb08e3e575f // indirect
	go.mozilla.org/pkcs7 v0.0.0-20200128120323-432b2356ecb1 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/otel/metric v1.21.0 // indirect
	go.opentelemetry.io/proto/otlp v1.0.0 // indirect
	golang.org/x/crypto v0.21.0 // indirect
	golang.org/x/mod v0.12.0 // indirect
	golang.org/x/net v0.23.0 // indirect
	golang.org/x/oauth2 v0.11.0 // indirect
	golang.org/x/term v0.18.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	golang.org/x/time v0.0.0-20220210224613-90d013bbcef8 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20231120223509-83a465c0220f // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	sigs.k8s.io/json v0.0.0-20220713155537-f223a00ba0e2 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.2.3 // indirect
	sigs.k8s.io/yaml v1.3.0 // indirect
	tags.cncf.io/container-device-interface/specs-go v0.7.0 // indirect
)

// Workaround for indirect dependency no longer being available.
// https://github.com/containerd/containerd/issues/9969
exclude github.com/mitchellh/osext v0.0.0-20151018003038-5e2d6d41470f
