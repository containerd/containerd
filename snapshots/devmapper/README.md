## Devmapper snapshotter

Devmapper is a `containerd` snapshotter plugin that stores snapshots in ext4-formatted filesystem images
in a devicemapper thin pool.

## Setup

To make it work you need to prepare `thin-pool` in advance and update containerd's configuration file.
This file is typically located at `/etc/containerd/config.toml`.

Here's minimal sample entry that can be made in the configuration file:

```
[plugins]
  ...
  [plugins.devmapper]
    pool_name = "containerd-pool"
    base_image_size = "128MB"
  ...
```

The following configuration flags are supported:
* `root_path` - a directory where the metadata will be available (if empty
  default location for `containerd` plugins will be used)
* `pool_name` - a name to use for the devicemapper thin pool. Pool name
  should be the same as in `/dev/mapper/` directory
* `base_image_size` - defines how much space to allocate when creating the base device

Pool name and base image size are required snapshotter parameters.

## Run
Give it a try with the following commands:

```bash
ctr images pull --snapshotter devmapper docker.io/library/hello-world:latest
ctr run --snapshotter devmapper docker.io/library/hello-world:latest test
```

## Requirements

The devicemapper snapshotter requires `dmsetup` (>= 1.02.110) command line tool to be installed and
available on your computer. On Ubuntu, it can be installed with `apt-get install dmsetup` command.