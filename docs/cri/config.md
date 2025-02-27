# CRI Plugin Config Guide
This document provides the description of the CRI plugin configuration.
The CRI plugin config is part of the containerd config (default
path: `/etc/containerd/config.toml`).

Additional Resources:

* [Ops and Admins](../ops.md)

* [Containerd Configurations Guide](https://github.com/containerd/containerd/blob/main/docs/man/containerd-config.toml.5.md)

* [Setting up containerd for Kubernetes](../getting-started.md#setting-up-containerd-for-kubernetes)

Note that the `[plugins."io.containerd.grpc.v1.cri"]` section is specific to CRI,
and not recognized by other containerd clients such as `ctr`, `nerdctl`, and Docker/Moby.

## Config versions
The content of `/etc/containerd/config.toml` must start with a version header, for example:
```toml
version = 3
```

The config version 3 was introduced in containerd v2.0.
The config version 2 used in containerd 1.x is still supported and automatically
converted to the config version 3.

For the further information, see [`../PLUGINS.md`](../PLUGINS.md).

## Basic configuration
### Cgroup Driver
While containerd and Kubernetes use the legacy `cgroupfs` driver for managing cgroups by default,
it is recommended to use the `systemd` driver on systemd-based hosts for compliance of
[the "single-writer" rule](https://systemd.io/CGROUP_DELEGATION/) of cgroups.

To configure containerd to use the `systemd` driver, set the following option in `/etc/containerd/config.toml`:
+ In containerd 2.x
```toml
version = 3
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc.options]
  SystemdCgroup = true
```
+ In containerd 1.x
```toml
version = 2
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
  SystemdCgroup = true
```

In addition to containerd, you have to configure the `KubeletConfiguration` to use the "systemd" cgroup driver.
The `KubeletConfiguration` is typically located at `/var/lib/kubelet/config.yaml`:
```yaml
kind: KubeletConfiguration
apiVersion: kubelet.config.k8s.io/v1beta1
cgroupDriver: "systemd"
```

kubeadm users should also see [the kubeadm documentation](https://kubernetes.io/docs/tasks/administer-cluster/kubeadm/configure-cgroup-driver/).

> Note: Kubernetes v1.28 supports automatic detection of the cgroup driver as
> an alpha feature. With the `KubeletCgroupDriverFromCRI` kubelet feature gate
> enabled, the kubelet automatically detects the cgroup driver from the CRI
> runtime and the `KubeletConfiguration` configuration step above is not
> needed.
>
> When determining the cgroup driver, containerd uses the `SystemdCgroup`
> setting from runc-based runtime classes, starting from the default runtime
> class. If no runc-based runtime classes have been configured containerd
> relies on auto-detection based on determining if systemd is running.
> Note that all runc-based runtime classes should be configured to have the
> same `SystemdCgroup` setting in order to avoid unexpected behavior.
>
> The automatic cgroup driver configuration for kubelet feature is supported in
> containerd v2.0 and later.

### Snapshotter

The default snapshotter is set to `overlayfs` (akin to Docker's `overlay2` storage driver):
+ In containerd 2.x
```toml
version = 3
[plugins.'io.containerd.cri.v1.images']
  snapshotter = "overlayfs"
```
+ In containerd 1.x
```toml
version = 2
[plugins."io.containerd.grpc.v1.cri".containerd]
  snapshotter = "overlayfs"
```

See [here](https://github.com/containerd/containerd/blob/main/docs/snapshotters) for other supported snapshotters.

### Runtime classes

The following example registers custom runtimes into containerd:
+ In containerd 2.x
```toml
version = 3
[plugins."io.containerd.cri.v1.runtime".containerd]
  default_runtime_name = "crun"
  [plugins."io.containerd.cri.v1.runtime".containerd.runtimes]
    # crun: https://github.com/containers/crun
    [plugins."io.containerd.cri.v1.runtime".containerd.runtimes.crun]
      runtime_type = "io.containerd.runc.v2"
      [plugins."io.containerd.cri.v1.runtime".containerd.runtimes.crun.options]
        BinaryName = "/usr/local/bin/crun"
    # gVisor: https://gvisor.dev/
    [plugins."io.containerd.cri.v1.runtime".containerd.runtimes.gvisor]
      runtime_type = "io.containerd.runsc.v1"
    # Kata Containers: https://katacontainers.io/
    [plugins."io.containerd.cri.v1.runtime".containerd.runtimes.kata]
      runtime_type = "io.containerd.kata.v2"
```
+ In containerd 1.x
```toml
version = 2
[plugins."io.containerd.grpc.v1.cri".containerd]
  default_runtime_name = "crun"
  [plugins."io.containerd.grpc.v1.cri".containerd.runtimes]
    # crun: https://github.com/containers/crun
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.crun]
      runtime_type = "io.containerd.runc.v2"
      [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.crun.options]
        BinaryName = "/usr/local/bin/crun"
    # gVisor: https://gvisor.dev/
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.gvisor]
      runtime_type = "io.containerd.runsc.v1"
    # Kata Containers: https://katacontainers.io/
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.kata]
      runtime_type = "io.containerd.kata.v2"
```

In addition, you have to install the following `RuntimeClass` resources into the cluster
with the `cluster-admin` role:

```yaml
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: crun
handler: crun
---
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: gvisor
handler: gvisor
---
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: kata
handler: kata
```

To apply a runtime class to a pod, set `.spec.runtimeClassName`:

```yaml
apiVersion: v1
kind: Pod
spec:
  runtimeClassName: crun
```

See also [the Kubernetes documentation](https://kubernetes.io/docs/concepts/containers/runtime-class/).

## Full configuration
The explanation and default value of each configuration item are as follows:
+ [Default CRI Configuration: Containerd 1.x section of the containerd config guide](../docs/containerd-config-cri-ex.x.md#Default-CRI-Configuration:-Containerd-1.x:).
+ [Default CRI Configuration: Containerd 2.x section of the containerd config guide](../docs/containerd-config-cri-ex.x.md#Default-CRI-Configuration:-Containerd-2.x:).

## Registry Configuration

Here is a simple example for a default registry hosts configuration. Set
`config_path = "/etc/containerd/certs.d"` in your config.toml for containerd.
Make a directory tree at the config path that includes `docker.io` as a directory
representing the host namespace to be configured. Then add a `hosts.toml` file
in the `docker.io` to configure the host namespace. It should look like this:
```
$ tree /etc/containerd/certs.d
/etc/containerd/certs.d
└── docker.io
    └── hosts.toml

$ cat /etc/containerd/certs.d/docker.io/hosts.toml
server = "https://docker.io"

[host."https://registry-1.docker.io"]
  capabilities = ["pull", "resolve"]
```

To specify a custom certificate:

```
$ cat /etc/containerd/certs.d/192.168.12.34:5000/hosts.toml
server = "https://192.168.12.34:5000"

[host."https://192.168.12.34:5000"]
  ca = "/path/to/ca.crt"
```

See [`docs/hosts.md`](https://github.com/containerd/containerd/blob/main/docs/hosts.md) for the further information.

## Untrusted Workload

The recommended way to run untrusted workload is to use
[`RuntimeClass`](https://kubernetes.io/docs/concepts/containers/runtime-class/) api
introduced in Kubernetes 1.12 to select RuntimeHandlers configured to run
untrusted workload in `plugins."io.containerd.grpc.v1.cri".containerd.runtimes`.

However, if you are using the legacy `io.kubernetes.cri.untrusted-workload`pod annotation
to request a pod be run using a runtime for untrusted workloads, the RuntimeHandler
`plugins."io.containerd.grpc.v1.cri"cri.containerd.runtimes.untrusted` must be defined first.
When the annotation `io.kubernetes.cri.untrusted-workload` is set to `true` the `untrusted`
runtime will be used. For example, see
[Create an untrusted pod using Kata Containers](https://github.com/kata-containers/kata-containers/blob/main/docs/how-to/containerd-kata.md#kata-containers-as-the-runtime-for-untrusted-workload).

## CNI Config Template

Ideally the cni config should be placed by system admin or cni daemon like calico, weaveworks etc.
However, this is useful for the cases when there is no cni daemonset to place cni config.

The cni config template uses the [golang
template](https://golang.org/pkg/text/template/) format. Currently supported
values are:
* `.PodCIDR` is a string of the first CIDR assigned to the node.
* `.PodCIDRRanges` is a string array of all CIDRs assigned to the node. It is
  usually used for
  [dualstack](https://github.com/kubernetes/enhancements/tree/master/keps/sig-network/563-dual-stack) support.
* `.Routes` is a string array of all routes needed. It is usually used for
  dualstack support or single stack but IPv4 or IPv6 is decided at runtime.

The [golang template actions](https://golang.org/pkg/text/template/#hdr-Actions)
can be used to render the cni config. For example, you can use the following
template to add CIDRs and routes for dualstack in the CNI config:
```
"ipam": {
  "type": "host-local",
  "ranges": [{{range $i, $range := .PodCIDRRanges}}{{if $i}}, {{end}}[{"subnet": "{{$range}}"}]{{end}}],
  "routes": [{{range $i, $route := .Routes}}{{if $i}}, {{end}}{"dst": "{{$route}}"}{{end}}]
}
```

## Deprecation
The config options of the CRI plugin follow the [Kubernetes deprecation
policy of "admin-facing CLI components"](https://kubernetes.io/docs/reference/using-api/deprecation-policy/#deprecating-a-flag-or-cli).

In summary, when a config option is announced to be deprecated:
* It is kept functional for 6 months or 1 release (whichever is longer);
* A warning is emitted when it is used.
