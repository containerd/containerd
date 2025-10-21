# CRI Plugin Config Guide
This document provides the description of the CRI plugin configuration.
The CRI plugin config is part of the containerd config (default
path: `/etc/containerd/config.toml`).

See [here](https://github.com/containerd/containerd/blob/main/docs/ops.md)
for more information about containerd config.

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


## Image Pull Configuration (since containerd v2.1)

### Transfer Service for Image Pull

Starting with containerd v2.1, the CRI plugin uses containerd's Transfer Service for image pull by default, instead of client-based pull.

To configure Transfer Service, use the following settings in your config.toml:

```toml
[plugins.'io.containerd.transfer.v1.local']
  # Transfer service specific configurations
  max_concurrent_downloads = 3
  unpack_config = { ... }
```

### Local Pull Mode

If you prefer to use the client-based pull method instead of the Transfer Service, you can set `use_local_image_pull = true` in your CRI image configuration:

```toml
[plugins.'io.containerd.cri.v1.images']
  use_local_image_pull = true
```

### Configuration differences and automatic fallback to Local Mode

There are some differences in how image pull configurations are specified between the Transfer Service and Local Pull mode:

| CRI Image Config Option | Local Pull | Transfer Service Pull |
|------------------------|------------|---------------------|
| Snapshotter | ✅ Supported | ✅ Supported |
| DisableSnapshotAnnotations | ✅ Supported | ⚠️ Must be configured in snapshotter plugin:<br>`[proxy_plugins.stargz.exports]`<br>`enable_remote_snapshot_annotations = "true"` |
| ImagePullProgressTimeout | ✅ Supported | ✅ Supported |
| DiscardUnpackedLayers | ✅ Supported | ❌ Not Supported |
| PinnedImages | ✅ Supported | ✅ Supported |
| Registry Settings | ✅ All supported | ⚠️ Only ConfigPath and Headers supported<br>(Mirrors, Configs, Auths not supported, also deprecated) |
| ImageDecryption | ❌ Disabled | ❌ Disabled |
| MaxConcurrentDownloads | ✅ Uses CRI Image config | ⚠️ Must be configured in transfer service plugin: `plugins."io.containerd.transfer.v1.local"` |
| ImagePullWithSyncFs | ✅ Supported | ❌ Not Supported |
| StatsCollectPeriod | ✅ Supported | ✅ Supported |

To ensure compatibility, ***containerd 2.1 automatically detects configuration conflicts and falls back to local image pull mode when necessary***.

If you have any of the following configurations in your CRI image config, containerd will automatically set `use_local_image_pull = true` and log a warning:

- `DisableSnapshotAnnotations = false`
- `DiscardUnpackedLayers = true`
- `Registry.Mirrors` is configured
- `Registry.Configs` is configured
- `Registry.Auths` is configured
- `MaxConcurrentDownloads != 3`
- `ImagePullWithSyncFs = true`

The warning message will indicate which configuration option triggered the fallback and provide guidance on how to properly configure the option when using the Transfer Service.

## Full configuration
The explanation and default value of each configuration item are as follows:
+ In containerd 2.x
<details>

<p>

```toml
# containerd has several configuration versions:
# - Version 3 (Recommended for containerd 2.x): Introduced in containerd 2.0.
#   Several plugin IDs have changed in this version.
# - Version 2 (Recommended for containerd 1.x): Introduced in containerd 1.3.
#   Still supported in containerd v2.x.
#   Plugin IDs are changed to have prefixes like "io.containerd.".
# - Version 1 (Default): Introduced in containerd 1.0. Removed in containerd 2.0.
version = 3

[plugins]
  [plugins.'io.containerd.cri.v1.images']
    snapshotter = 'overlayfs'
    disable_snapshot_annotations = true
    discard_unpacked_layers = false
    max_concurrent_downloads = 3
    image_pull_progress_timeout = '5m0s'
    image_pull_with_sync_fs = false
    stats_collect_period = 10
    use_local_image_pull = false

    [plugins.'io.containerd.cri.v1.images'.pinned_images]
      sandbox = 'registry.k8s.io/pause:3.10.1'

    [plugins.'io.containerd.cri.v1.images'.registry]
      config_path = ''

    [plugins.'io.containerd.cri.v1.images'.image_decryption]
      key_model = 'node'

  [plugins.'io.containerd.cri.v1.runtime']
    enable_selinux = false
    selinux_category_range = 1024
    max_container_log_line_size = 16384
    disable_cgroup = false
    disable_apparmor = false
    restrict_oom_score_adj = false
    disable_proc_mount = false
    unset_seccomp_profile = ''
    tolerate_missing_hugetlb_controller = true
    disable_hugetlb_controller = true
    device_ownership_from_security_context = false
    ignore_image_defined_volumes = false
    netns_mounts_under_state_dir = false
    enable_unprivileged_ports = true
    enable_unprivileged_icmp = true
    enable_cdi = true
    cdi_spec_dirs = ['/etc/cdi', '/var/run/cdi']
    drain_exec_sync_io_timeout = '0s'
    ignore_deprecation_warnings = []

    [plugins.'io.containerd.cri.v1.runtime'.containerd]
      default_runtime_name = 'runc'
      ignore_blockio_not_enabled_errors = false
      ignore_rdt_not_enabled_errors = false

      [plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes]
        [plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc]
          runtime_type = 'io.containerd.runc.v2'
          runtime_path = ''
          pod_annotations = []
          container_annotations = []
          privileged_without_host_devices = false
          privileged_without_host_devices_all_devices_allowed = false
          cgroup_writable = false
          base_runtime_spec = ''
          cni_conf_dir = ''
          cni_max_conf_num = 0
          snapshotter = ''
          sandboxer = 'podsandbox'
          io_type = ''

          [plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc.options]
            BinaryName = ''
            CriuImagePath = ''
            CriuWorkPath = ''
            IoGid = 0
            IoUid = 0
            NoNewKeyring = false
            Root = ''
            ShimCgroup = ''

    [plugins.'io.containerd.cri.v1.runtime'.cni]
      # DEPRECATED, use `bin_dirs` instead (since containerd v2.1).
      bin_dir = ''
      bin_dirs = ['/opt/cni/bin']
      conf_dir = '/etc/cni/net.d'
      max_conf_num = 1
      setup_serially = false
      conf_template = ''
      ip_pref = ''
      use_internal_loopback = false

  [plugins.'io.containerd.grpc.v1.cri']
    disable_tcp_service = true
    stream_server_address = '127.0.0.1'
    stream_server_port = '0'
    stream_idle_timeout = '4h0m0s'
    enable_tls_streaming = false

    [plugins.'io.containerd.grpc.v1.cri'.x509_key_pair_streaming]
      tls_cert_file = ''
      tls_key_file = ''
```

</p>
</details>

+ In containerd 1.x
<details>

<p>

```toml
# containerd has several configuration versions:
# - Version 3 (Recommended for containerd 2.x): Introduced in containerd 2.0.
#   Several plugin IDs have changed in this version.
# - Version 2 (Recommended for containerd 1.x): Introduced in containerd 1.3.
#   Still supported in containerd v2.x.
#   Plugin IDs are changed to have prefixes like "io.containerd.".
# - Version 1 (Default): Introduced in containerd 1.0. Removed in containerd 2.0.
version = 2

# The 'plugins."io.containerd.grpc.v1.cri"' table contains all of the server options.
[plugins."io.containerd.grpc.v1.cri"]

  # disable_tcp_service disables serving CRI on the TCP server.
  # Note that a TCP server is enabled for containerd if TCPAddress is set in section [grpc].
  disable_tcp_service = true

  # stream_server_address is the ip address streaming server is listening on.
  stream_server_address = "127.0.0.1"

  # stream_server_port is the port streaming server is listening on.
  stream_server_port = "0"

  # stream_idle_timeout is the maximum time a streaming connection can be
  # idle before the connection is automatically closed.
  # The string is in the golang duration format, see:
  #   https://golang.org/pkg/time/#ParseDuration
  stream_idle_timeout = "4h"

  # enable_selinux indicates to enable the selinux support.
  enable_selinux = false

  # selinux_category_range allows the upper bound on the category range to be set.
  # if not specified or set to 0, defaults to 1024 from the selinux package.
  selinux_category_range = 1024

  # sandbox_image is the image used by sandbox container.
  sandbox_image = "registry.k8s.io/pause:3.10.1"

  # stats_collect_period is the period (in seconds) of snapshots stats collection.
  stats_collect_period = 10

  # enable_tls_streaming enables the TLS streaming support.
  # It generates a self-sign certificate unless the following x509_key_pair_streaming are both set.
  enable_tls_streaming = false

  # tolerate_missing_hugetlb_controller if set to false will error out on create/update
  # container requests with huge page limits if the cgroup controller for hugepages is not present.
  # This helps with supporting Kubernetes <=1.18 out of the box. (default is `true`)
  tolerate_missing_hugetlb_controller = true

  # ignore_image_defined_volumes ignores volumes defined by the image. Useful for better resource
  # isolation, security and early detection of issues in the mount configuration when using
  # ReadOnlyRootFilesystem since containers won't silently mount a temporary volume.
  ignore_image_defined_volumes = false

  # netns_mounts_under_state_dir places all mounts for network namespaces under StateDir/netns
  # instead of being placed under the hardcoded directory /var/run/netns. Changing this setting
  # requires that all containers are deleted.
  netns_mounts_under_state_dir = false

  # max_container_log_line_size is the maximum log line size in bytes for a container.
  # Log line longer than the limit will be split into multiple lines. -1 means no
  # limit.
  max_container_log_line_size = 16384

  # disable_cgroup indicates to disable the cgroup support.
  # This is useful when the daemon does not have permission to access cgroup.
  disable_cgroup = false

  # disable_apparmor indicates to disable the apparmor support.
  # This is useful when the daemon does not have permission to access apparmor.
  disable_apparmor = false

  # restrict_oom_score_adj indicates to limit the lower bound of OOMScoreAdj to
  # the containerd's current OOMScoreAdj.
  # This is useful when the containerd does not have permission to decrease OOMScoreAdj.
  restrict_oom_score_adj = false

  # max_concurrent_downloads restricts the number of concurrent downloads for each image.
  max_concurrent_downloads = 3

  # disable_proc_mount disables Kubernetes ProcMount support. This MUST be set to `true`
  # when using containerd with Kubernetes <=1.11.
  disable_proc_mount = false

  # unset_seccomp_profile is the seccomp profile containerd/cri will use if the seccomp
  # profile requested over CRI is unset (or nil) for a pod/container (otherwise if this field is not set the
  # default unset profile will map to `unconfined`)
    # Note: The default unset seccomp profile should not be confused with the seccomp profile
    # used in CRI when the runtime default seccomp profile is requested. In the later case, the
    # default is set by the following code (https://github.com/containerd/containerd/blob/main/contrib/seccomp/seccomp_default.go).
    # To summarize, there are two different seccomp defaults, the unset default used when the CRI request is
    # set to nil or `unconfined`, and the default used when the runtime default seccomp profile is requested.
  unset_seccomp_profile = ""

  # enable_unprivileged_ports configures net.ipv4.ip_unprivileged_port_start=0
  # for all containers which are not using host network
  # and if it is not overwritten by PodSandboxConfig
  # Note that before containerd v2.0, this value defaulted to false.
  #   [k8s discussion](https://github.com/kubernetes/kubernetes/issues/102612)
  enable_unprivileged_ports = true

  # enable_unprivileged_icmp configures net.ipv4.ping_group_range="0 2147483647"
  # for all containers which are not using host network, are not running in user namespace
  # and if it is not overwritten by PodSandboxConfig
  # Note that before containerd v2.0, this value defaulted to false.
  enable_unprivileged_icmp = true

  # enable_cdi enables support of the Container Device Interface (CDI)
  # For more details about CDI and the syntax of CDI Spec files please refer to
  # https://tags.cncf.io/container-device-interface.
  # TODO: Deprecate this option when either Dynamic Resource Allocation(DRA)
  # or CDI support for the Device Plugins are graduated to GA.
  # `Dynamic Resource Allocation` KEP:
  # https://github.com/kubernetes/enhancements/tree/master/keps/sig-node/3063-dynamic-resource-allocation
  # `Add CDI devices to device plugin API` KEP:
  # https://github.com/kubernetes/enhancements/tree/master/keps/sig-node/4009-add-cdi-devices-to-device-plugin-api
  enable_cdi = true

  # cdi_spec_dirs is the list of directories to scan for CDI spec files
  # For more details about CDI configuration please refer to
  # https://tags.cncf.io/container-device-interface#containerd-configuration
  cdi_spec_dirs = ["/etc/cdi", "/var/run/cdi"]

  # drain_exec_sync_io_timeout is the maximum duration to wait for ExecSync API'
  # IO EOF event after exec init process exits. A zero value means there is no
  # timeout.
  #
  # The string is in the golang duration format, see:
  #    https://golang.org/pkg/time/#ParseDuration
  #
  # For example, the value can be '5h', '2h30m', '10s'.
  drain_exec_sync_io_timeout = "0s"

  # 'plugins."io.containerd.grpc.v1.cri".x509_key_pair_streaming' contains a x509 valid key pair to stream with tls.
  [plugins."io.containerd.grpc.v1.cri".x509_key_pair_streaming]
    # tls_cert_file is the filepath to the certificate paired with the "tls_key_file"
    tls_cert_file = ""

    # tls_key_file is the filepath to the private key paired with the "tls_cert_file"
    tls_key_file = ""

  # 'plugins."io.containerd.grpc.v1.cri".containerd' contains config related to containerd
  [plugins."io.containerd.grpc.v1.cri".containerd]

    # snapshotter is the default snapshotter used by containerd
    # for all runtimes, if not overridden by an experimental runtime's snapshotter config.
    snapshotter = "overlayfs"

    # no_pivot disables pivot-root (linux only), required when running a container in a RamDisk with runc.
    # This only works for runtime type "io.containerd.runtime.v1.linux".
    no_pivot = false

    # disable_snapshot_annotations disables to pass additional annotations (image
    # related information) to snapshotters. These annotations are required by
    # stargz snapshotter (https://github.com/containerd/stargz-snapshotter)
    # changed to default true with https://github.com/containerd/containerd/pull/4665 and subsequent service refreshes.
    disable_snapshot_annotations = true

    # discard_unpacked_layers allows GC to remove layers from the content store after
    # successfully unpacking these layers to the snapshotter.
    discard_unpacked_layers = false

    # default_runtime_name is the default runtime name to use.
    default_runtime_name = "runc"

    # ignore_blockio_not_enabled_errors disables blockio related
    # errors when blockio support has not been enabled. By default,
    # trying to set the blockio class of a container via annotations
    # produces an error if blockio hasn't been enabled.  This config
    # option practically enables a "soft" mode for blockio where these
    # errors are ignored and the container gets no blockio class.
    ignore_blockio_not_enabled_errors = false

    # ignore_rdt_not_enabled_errors disables RDT related errors when RDT
    # support has not been enabled. Intel RDT is a technology for cache and
    # memory bandwidth management. By default, trying to set the RDT class of
    # a container via annotations produces an error if RDT hasn't been enabled.
    # This config option practically enables a "soft" mode for RDT where these
    # errors are ignored and the container gets no RDT class.
    ignore_rdt_not_enabled_errors = false

    # 'plugins."io.containerd.grpc.v1.cri".containerd.default_runtime' is the runtime to use in containerd.
    # DEPRECATED: use `default_runtime_name` and `plugins."io.containerd.grpc.v1.cri".containerd.runtimes` instead.
    [plugins."io.containerd.grpc.v1.cri".containerd.default_runtime]

    # 'plugins."io.containerd.grpc.v1.cri".containerd.untrusted_workload_runtime' is a runtime to run untrusted workloads on it.
    # DEPRECATED: use `untrusted` runtime in `plugins."io.containerd.grpc.v1.cri".containerd.runtimes` instead.
    [plugins."io.containerd.grpc.v1.cri".containerd.untrusted_workload_runtime]

    # 'plugins."io.containerd.grpc.v1.cri".containerd.runtimes' is a map from CRI RuntimeHandler strings, which specify types
    # of runtime configurations, to the matching configurations.
    # In this example, 'runc' is the RuntimeHandler string to match.
    [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
      # runtime_type is the runtime type to use in containerd.
      # The default value is "io.containerd.runc.v2" since containerd 1.4.
      # The default value was "io.containerd.runc.v1" in containerd 1.3, "io.containerd.runtime.v1.linux" in prior releases.
      runtime_type = "io.containerd.runc.v2"

      # runtime_path is an optional field that can be used to overwrite path to a shim runtime binary.
      # When specified, containerd will ignore runtime name field when resolving shim location.
      # Path must be abs.
      runtime_path = ""

      # pod_annotations is a list of pod annotations passed to both pod
      # sandbox as well as container OCI annotations. Pod_annotations also
      # supports golang path match pattern - https://golang.org/pkg/path/#Match.
      # e.g. ["runc.com.*"], ["*.runc.com"], ["runc.com/*"].
      #
      # For the naming convention of annotation keys, please reference:
      # * Kubernetes: https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations/#syntax-and-character-set
      # * OCI: https://github.com/opencontainers/image-spec/blob/main/annotations.md
      pod_annotations = []

      # container_annotations is a list of container annotations passed through to the OCI config of the containers.
      # Container annotations in CRI are usually generated by other Kubernetes node components (i.e., not users).
      # Currently, only device plugins populate the annotations.
      container_annotations = []

      # privileged_without_host_devices allows overloading the default behaviour of passing host
      # devices through to privileged containers. This is useful when using a runtime where it does
      # not make sense to pass host devices to the container when privileged. Defaults to false -
      # i.e pass host devices through to privileged containers.
      privileged_without_host_devices = false

      # privileged_without_host_devices_all_devices_allowed allows the allowlisting of all devices when
      # privileged_without_host_devices is enabled.
      # In plain privileged mode all host device nodes are added to the container's spec and all devices
      # are put in the container's device allowlist. This flags is for the modification of the privileged_without_host_devices
      # option so that even when no host devices are implicitly added to the container, all devices allowlisting is still enabled.
      # Requires privileged_without_host_devices to be enabled. Defaults to false.
      privileged_without_host_devices_all_devices_allowed = false

      # cgroup_writable field enables the support for writable cgroups in unprivileged containers with cgroup v2 enabled. When disabled, the cgroup interface (/sys/fs/cgroup) is mounted as read-only, preventing containers from managing their own cgroup hierarchies.
      cgroup_writable = false

      # base_runtime_spec is a file path to a JSON file with the OCI spec that will be used as the base spec that all
      # container's are created from.
      # Use containerd's `ctr oci spec > /etc/containerd/cri-base.json` to output initial spec file.
      # Spec files are loaded at launch, so containerd daemon must be restarted on any changes to refresh default specs.
      # Still running containers and restarted containers will still be using the original spec from which that container was created.
      base_runtime_spec = ""

      # conf_dir is the directory in which the admin places a CNI conf.
      # this allows a different CNI conf for the network stack when a different runtime is being used.
      cni_conf_dir = "/etc/cni/net.d"

      # cni_max_conf_num specifies the maximum number of CNI plugin config files to
      # load from the CNI config directory. By default, only 1 CNI plugin config
      # file will be loaded. If you want to load multiple CNI plugin config files
      # set max_conf_num to the number desired. Setting cni_max_config_num to 0 is
      # interpreted as no limit is desired and will result in all CNI plugin
      # config files being loaded from the CNI config directory.
      cni_max_conf_num = 1

      # snapshotter overrides the global default snapshotter to a runtime specific value.
      # Please be aware that overriding the default snapshotter on a runtime basis is currently an experimental feature.
      # See https://github.com/containerd/containerd/issues/6657 for context.
      snapshotter = ""

      # sandboxer is the sandbox controller for the runtime.
      # The default sandbox controller is the podsandbox controller, which create a "pause" container as a sandbox.
      # We can create our own "shim" sandbox controller by implementing the sandbox api defined in runtime/sandbox/v1/sandbox.proto in our shim, and specifiy the sandboxer to "shim" here.
      # We can also run a grpc or ttrpc server to serve the sandbox controller API defined in services/sandbox/v1/sandbox.proto, and define a ProxyPlugin of "sandbox" type, and specify the name of the ProxyPlugin here.
      sandboxer = ""

      # io_type is the way containerd get stdin/stdout/stderr from container or the execed process.
      # The default value is "fifo", in which containerd will create a set of named pipes and transfer io by them.
      # Currently the value of "streaming" is supported, in this way, sandbox should serve streaming api defined in services/streaming/v1/streaming.proto, and containerd will connect to sandbox's endpoint and create a set of streams to it, as channels to transfer io of container or process.
      io_type = ""

      # 'plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options' is options specific to
      # "io.containerd.runc.v1" and "io.containerd.runc.v2". Its corresponding options type is:
      #   https://github.com/containerd/containerd/blob/v1.3.2/runtime/v2/runc/options/oci.pb.go#L26 .
      [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
        # NoPivotRoot disables pivot root when creating a container.
        NoPivotRoot = false

        # NoNewKeyring disables new keyring for the container.
        NoNewKeyring = false

        # ShimCgroup places the shim in a cgroup.
        ShimCgroup = ""

        # IoUid sets the I/O's pipes uid.
        IoUid = 0

        # IoGid sets the I/O's pipes gid.
        IoGid = 0

        # BinaryName is the binary name of the runc binary.
        BinaryName = ""

        # Root is the runc root directory.
        Root = ""

        # SystemdCgroup enables systemd cgroups.
        SystemdCgroup = false

        # CriuImagePath is the criu image path
        CriuImagePath = ""

        # CriuWorkPath is the criu work path.
        CriuWorkPath = ""

  # 'plugins."io.containerd.grpc.v1.cri".cni' contains config related to cni
  [plugins."io.containerd.grpc.v1.cri".cni]
    # bin_dir is the directory in which the binaries for the plugin is kept.
    bin_dir = "/opt/cni/bin"

    # conf_dir is the directory in which the admin places a CNI conf.
    conf_dir = "/etc/cni/net.d"

    # max_conf_num specifies the maximum number of CNI plugin config files to
    # load from the CNI config directory. By default, only 1 CNI plugin config
    # file will be loaded. If you want to load multiple CNI plugin config files
    # set max_conf_num to the number desired. Setting max_config_num to 0 is
    # interpreted as no limit is desired and will result in all CNI plugin
    # config files being loaded from the CNI config directory.
    max_conf_num = 1

    # conf_template is the file path of golang template used to generate
    # cni config.
    # If this is set, containerd will generate a cni config file from the
    # template. Otherwise, containerd will wait for the system admin or cni
    # daemon to drop the config file into the conf_dir.
    # See the "CNI Config Template" section for more details.
    conf_template = ""
    # ip_pref specifies the strategy to use when selecting the main IP address for a pod.
    # options include:
    # * ipv4, "" - (default) select the first ipv4 address
    # * ipv6 - select the first ipv6 address
    # * cni - use the order returned by the CNI plugins, returning the first IP address from the results
    ip_pref = "ipv4"
    # use_internal_loopback specifies if we use the CNI loopback plugin or internal mechanism to set lo to up
    use_internal_loopback = false

  # 'plugins."io.containerd.grpc.v1.cri".image_decryption' contains config related
  # to handling decryption of encrypted container images.
  [plugins."io.containerd.grpc.v1.cri".image_decryption]
    # key_model defines the name of the key model used for how the cri obtains
    # keys used for decryption of encrypted container images.
    # The [decryption document](https://github.com/containerd/containerd/blob/main/docs/cri/decryption.md)
    # contains additional information about the key models available.
    #
    # Set of available string options: {"", "node"}
    # Omission of this field defaults to the empty string "", which indicates no key model,
    # disabling image decryption.
    #
    # In order to use the decryption feature, additional configurations must be made.
    # The [decryption document](https://github.com/containerd/containerd/blob/main/docs/cri/decryption.md)
    # provides information of how to set up stream processors and the containerd imgcrypt decoder
    # with the appropriate key models.
    #
    # Additional information:
    # * Stream processors: https://github.com/containerd/containerd/blob/main/docs/stream_processors.md
    # * Containerd imgcrypt: https://github.com/containerd/imgcrypt
    key_model = "node"

  # 'plugins."io.containerd.grpc.v1.cri".registry' contains config related to
  # the registry
  [plugins."io.containerd.grpc.v1.cri".registry]
    # config_path specifies a directory to look for the registry hosts configuration.
    #
    # The cri plugin will look for and use config_path/host-namespace/hosts.toml
    #   configs if present OR load certificate files as laid out in the Docker/Moby
    #   specific layout https://docs.docker.com/engine/security/certificates/
    #
    # If config_path is not provided defaults are used.
    #
    # *** registry.configs and registry.mirrors that were a part of containerd 1.4
    # are now DEPRECATED and will only be used if the config_path is not specified.
    config_path = ""
```

</p>
</details>

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
