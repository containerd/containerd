# CRI Plugin Config Guide
This document provides the description of the CRI plugin configuration.

The explanation and default value of each configuration item are as follows:
```toml
# The "plugins.cri" table contains all of the server options.
[plugins.cri]

  # stream_server_address is the ip address streaming server is listening on.
  stream_server_address = "127.0.0.1"

  # stream_server_port is the port streaming server is listening on.
  stream_server_port = "0"

  # enable_selinux indicates to enable the selinux support.
  enable_selinux = false

  # sandbox_image is the image used by sandbox container.
  sandbox_image = "k8s.gcr.io/pause:3.1"

  # stats_collect_period is the period (in seconds) of snapshots stats collection.
  stats_collect_period = 10

  # systemd_cgroup enables systemd cgroup support. This only works for runtime
  # type "io.containerd.runtime.v1.linux".
  # DEPRECATED: use Runtime.Options for runtime specific config for shim v2 runtimes.
  #   For runtime "io.containerd.runc.v1", use the option `SystemdCgroup`.
  systemd_cgroup = false

  # enable_tls_streaming enables the TLS streaming support.
  # It generates a self-sign certificate unless the following x509_key_pair_streaming are both set.
  enable_tls_streaming = false

  # "plugins.cri.x509_key_pair_streaming" contains a x509 valid key pair to stream with tls.
  [plugins.cri.x509_key_pair_streaming]
    # tls_cert_file is the filepath to the certificate paired with the "tls_key_file"
    tls_cert_file = ""

    # tls_key_file is the filepath to the private key paired with the "tls_cert_file"
    tls_key_file = ""

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

  # "plugins.cri.containerd" contains config related to containerd
  [plugins.cri.containerd]

    # snapshotter is the snapshotter used by containerd.
    snapshotter = "overlayfs"

    # no_pivot disables pivot-root (linux only), required when running a container in a RamDisk with runc.
    # This only works for runtime type "io.containerd.runtime.v1.linux".
    # DEPRECATED: use Runtime.Options for runtime specific config for shim v2 runtimes.
    #   For runtime "io.containerd.runc.v1", use the option `NoPivotRoot`.
    no_pivot = false

    # "plugins.cri.containerd.default_runtime" is the runtime to use in containerd.
    [plugins.cri.containerd.default_runtime]
      # runtime_type is the runtime type to use in containerd e.g. io.containerd.runtime.v1.linux
      runtime_type = "io.containerd.runtime.v1.linux"

      # runtime_engine is the name of the runtime engine used by containerd.
      # This only works for runtime type "io.containerd.runtime.v1.linux".
      # DEPRECATED: use Runtime.Options for runtime specific config for shim v2 runtimes.
      #   For runtime "io.containerd.runc.v1", use the option `BinaryName`.
      runtime_engine = ""

      # runtime_root is the directory used by containerd for runtime state.
      # This only works for runtime type "io.containerd.runtime.v1.linux".
      # DEPRECATED: use Runtime.Options for runtime specific config for shim v2 runtimes.
      #   For runtime "io.containerd.runc.v1", use the option `Root`.
      runtime_root = ""

      # "plugins.cri.containerd.default_runtime.options" is options specific to
      # the default runtime. The options type for "io.containerd.runtime.v1.linux" is:
      #   https://github.com/containerd/containerd/blob/v1.2.0-rc.1/runtime/linux/runctypes/runc.pb.go#L40
      # NOTE: when `options` is specified, all related deprecated options will
      #   be ignored, including `systemd_cgroup`, `no_pivot`, `runtime_engine`
      #   and `runtime_root`.
      [plugins.cri.containerd.default_runtime.options]
        # Runtime is the binary name of the runtime.
        Runtime = ""

        # RuntimeRoot is the root directory of the runtime.
        RuntimeRoot = ""

        # CriuPath is the criu binary path.
        CriuPath = ""

        # SystemdCgroup enables systemd cgroups.
        SystemdCgroup = false

    # "plugins.cri.containerd.untrusted_workload_runtime" is a runtime to run untrusted workloads on it.
    # DEPRECATED: use plugins.cri.runtimes instead. If provided, this runtime is mapped to the
    #   runtime handler named 'untrusted'. It is a configuration error to provide both the (now
    #   deprecated) UntrustedWorkloadRuntime and a handler in the Runtimes handler map (below) for
    #   'untrusted' workloads at the same time. Please provide one or the other.
    [plugins.cri.containerd.untrusted_workload_runtime]
      # runtime_type is the runtime type to use in containerd e.g. io.containerd.runtime.v1.linux
      runtime_type = ""

      # runtime_engine is the name of the runtime engine used by containerd.
      runtime_engine = ""

      # runtime_root is the directory used by containerd for runtime state.
      runtime_root = ""

    # plugins.cri.containerd.runtimes is a map from CRI RuntimeHandler strings, which specify types
    # of runtime configurations, to the matching configurations. In this example,
    # 'runc' is the RuntimeHandler string to match.
    [plugins.cri.containerd.runtimes.runc]
      # runtime_type is the runtime type to use in containerd e.g. io.containerd.runtime.v1.linux
      runtime_type = "io.containerd.runc.v1"

      # "plugins.cri.containerd.runtimes.runc.options" is options specific to
      # "io.containerd.runc.v1". Its corresponding options type is:
      #   https://github.com/containerd/containerd/blob/v1.2.0-rc.1/runtime/v2/runc/options/oci.pb.go#L39.
      [plugins.cri.containerd.runtimes.runc.options]
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

        # CriuPath is the criu binary path.
        CriuPath = ""

        # SystemdCgroup enables systemd cgroups.
        SystemdCgroup = false

  # "plugins.cri.cni" contains config related to cni
  [plugins.cri.cni]
    # bin_dir is the directory in which the binaries for the plugin is kept.
    bin_dir = "/opt/cni/bin"

    # conf_dir is the directory in which the admin places a CNI conf.
    conf_dir = "/etc/cni/net.d"

    # conf_template is the file path of golang template used to generate
    # cni config.
    # If this is set, containerd will generate a cni config file from the
    # template. Otherwise, containerd will wait for the system admin or cni
    # daemon to drop the config file into the conf_dir.
    # This is a temporary backward-compatible solution for kubenet users
    # who don't have a cni daemonset in production yet.
    # This will be deprecated when kubenet is deprecated.
    conf_template = ""

  # "plugins.cri.registry" contains config related to the registry
  [plugins.cri.registry]

    # "plugins.cri.registry.mirrors" are namespace to mirror mapping for all namespaces.
    [plugins.cri.registry.mirrors]
      [plugins.cri.registry.mirrors."docker.io"]
        endpoint = ["https://registry-1.docker.io", ]
```
