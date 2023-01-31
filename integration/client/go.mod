module github.com/containerd/containerd/integration/client

go 1.18

require (
	github.com/AdaLogics/go-fuzz-headers v0.0.0-20230106234847-43070de90fa1
	github.com/Microsoft/hcsshim v0.10.0-rc.4
	github.com/Microsoft/hcsshim/test v0.0.0-20210408205431-da33ecd607e1
	github.com/containerd/cgroups/v3 v3.0.0
	github.com/containerd/containerd v1.7.0-beta.0 // see replace; the actual version of containerd is replaced with the code at the root of this repository
	github.com/containerd/go-runc v1.0.0
	github.com/containerd/ttrpc v1.1.1-0.20220420014843-944ef4a40df3
	github.com/containerd/typeurl v1.0.3-0.20220422153119-7f6e6d160d67
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.1.0-rc2.0.20221005185240-3a7f492d3f1b
	github.com/opencontainers/runtime-spec v1.0.3-0.20220825212826-86290f6a00fb
	github.com/sirupsen/logrus v1.9.0
	github.com/stretchr/testify v1.8.1
	go.opentelemetry.io/otel v1.12.0
	go.opentelemetry.io/otel/sdk v1.12.0
	golang.org/x/sys v0.4.0
)

require (
	github.com/AdamKorcz/go-118-fuzz-build v0.0.0-20221215162035-5330a85ea652 // indirect
	github.com/Microsoft/go-winio v0.6.0 // indirect
	github.com/cilium/ebpf v0.9.1 // indirect
	github.com/containerd/cgroups v1.0.4 // indirect
	github.com/containerd/console v1.0.3 // indirect
	github.com/containerd/continuity v0.3.0 // indirect
	github.com/containerd/fifo v1.0.0 // indirect
	github.com/coreos/go-systemd/v22 v22.5.0 // indirect
	github.com/cyphar/filepath-securejoin v0.2.3 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/docker/go-events v0.0.0-20190806004212-e31b211e4f1c // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/go-logr/logr v1.2.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/go-cmp v0.5.9 // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/imdario/mergo v0.3.12 // indirect
	github.com/klauspost/compress v1.15.11 // indirect
	github.com/moby/locker v1.0.1 // indirect
	github.com/moby/sys/mountinfo v0.6.2 // indirect
	github.com/moby/sys/sequential v0.5.0 // indirect
	github.com/moby/sys/signal v0.7.0 // indirect
	github.com/opencontainers/runc v1.1.4 // indirect
	github.com/opencontainers/selinux v1.10.2 // indirect
	github.com/pelletier/go-toml v1.9.5 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/otel/trace v1.12.0 // indirect
	golang.org/x/mod v0.7.0 // indirect
	golang.org/x/net v0.5.0 // indirect
	golang.org/x/sync v0.1.0 // indirect
	golang.org/x/text v0.6.0 // indirect
	golang.org/x/tools v0.5.0 // indirect
	google.golang.org/genproto v0.0.0-20221206210731-b1a01be3a5f6 // indirect
	google.golang.org/grpc v1.52.0 // indirect
	google.golang.org/protobuf v1.28.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// use the containerd module from this repository instead of downloading
//
// IMPORTANT: this replace rule ONLY replaces containerd itself; dependencies
// in the "require" section above are still taken into account for version
// resolution if newer.
replace (
	github.com/containerd/containerd => ../../

	// ATM the runtime-tools commit we need are beyond the latest tag.
	// We use a replace to handle that until a new version is tagged.
	github.com/opencontainers/runtime-tools => github.com/opencontainers/runtime-tools v0.0.0-20221026201742-946c877fa809
)

replace github.com/AdaLogics/go-fuzz-headers => github.com/AdamKorcz/go-fuzz-headers-1 v0.0.0-20230111232327-1f10f66a31bf
