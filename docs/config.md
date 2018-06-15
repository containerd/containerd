# CRI Plugin Config Guide
This document provides the description of the CRI plugin configuration.

The explanation and default value of each configuration item are as follows:
```toml
# The "plugins.cri" table contains all of the server options.
[plugins.cri]

  # stream_server_address is the ip address streaming server is listening on.
  stream_server_address = ""

  # stream_server_port is the port streaming server is listening on.
  stream_server_port = "10010"

  # enable_selinux indicates to enable the selinux support.
  enable_selinux = false

  # sandbox_image is the image used by sandbox container.
  sandbox_image = "k8s.gcr.io/pause:3.1"

  # stats_collect_period is the period (in seconds) of snapshots stats collection.
  stats_collect_period = 10

  # systemd_cgroup enables systemd cgroup support.
  systemd_cgroup = false

  # enable_tls_streaming enables the TLS streaming support.
  enable_tls_streaming = false

  # max_container_log_line_size is the maximum log line size in bytes for a container.
  # Log line longer than the limit will be split into multiple lines. -1 means no
  # limit.
  max_container_log_line_size = 16384

  # "plugins.cri.containerd" contains config related to containerd
  [plugins.cri.containerd]

    # snapshotter is the snapshotter used by containerd.
    snapshotter = "overlayfs"

    # "plugins.cri.containerd.default_runtime" is the runtime to use in containerd.
    [plugins.cri.containerd.default_runtime]
      # runtime_type is the runtime type to use in containerd e.g. io.containerd.runtime.v1.linux
      runtime_type = "io.containerd.runtime.v1.linux"

      # runtime_engine is the name of the runtime engine used by containerd.
      runtime_engine = ""

      # runtime_root is the directory used by containerd for runtime state.
      runtime_root = ""

    # "plugins.cri.containerd.untrusted_workload_runtime" is a runtime to run untrusted workloads on it.
    [plugins.cri.containerd.untrusted_workload_runtime]
      # runtime_type is the runtime type to use in containerd e.g. io.containerd.runtime.v1.linux
      runtime_type = ""

      # runtime_engine is the name of the runtime engine used by containerd.
      runtime_engine = ""

      # runtime_root is the directory used by containerd for runtime state.
      runtime_root = ""

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
