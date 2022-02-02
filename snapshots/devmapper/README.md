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
    base_image_size = "8192MB"
  ...
```

The following configuration flags are supported:
* `root_path` - a directory where the metadata will be available (if empty
  default location for `containerd` plugins will be used)
* `pool_name` - a name to use for the devicemapper thin pool. Pool name
  should be the same as in `/dev/mapper/` directory
* `base_image_size` - defines how much space to allocate when creating the base device
* `async_remove` - flag to async remove device using snapshot GC's cleanup callback
* `discard_blocks` - whether to discard blocks when removing a device. This is especially useful for returning disk space to the filesystem when using loopback devices.
* `fs_type` - defines the file system to use for snapshot device mount. Valid values are `ext4` and `xfs`. Defaults to `ext4` if unspecified.
* `fs_options` - optionally defines the file system options. This is currently only applicable to `ext4` file system.

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

### How to setup device mapper thin-pool

There are many ways how to configure a devmapper thin-pool depending on your requirements, disk configuration,
and environment.

On local dev environment you can utilize loopback devices. This type of configuration is simple and suits well for
development and testing (please note that this configuration is slow and not recommended for production uses).
Run the following script to create a thin-pool device:

```bash
#!/bin/bash
set -ex

DATA_DIR=/var/lib/containerd/devmapper
POOL_NAME=devpool

mkdir -p ${DATA_DIR}

# Create data file
sudo touch "${DATA_DIR}/data"
sudo truncate -s 100G "${DATA_DIR}/data"

# Create metadata file
sudo touch "${DATA_DIR}/meta"
sudo truncate -s 10G "${DATA_DIR}/meta"

# Allocate loop devices
DATA_DEV=$(sudo losetup --find --show "${DATA_DIR}/data")
META_DEV=$(sudo losetup --find --show "${DATA_DIR}/meta")

# Define thin-pool parameters.
# See https://www.kernel.org/doc/Documentation/device-mapper/thin-provisioning.txt for details.
SECTOR_SIZE=512
DATA_SIZE="$(sudo blockdev --getsize64 -q ${DATA_DEV})"
LENGTH_IN_SECTORS=$(bc <<< "${DATA_SIZE}/${SECTOR_SIZE}")
DATA_BLOCK_SIZE=128
LOW_WATER_MARK=32768

# Create a thin-pool device
sudo dmsetup create "${POOL_NAME}" \
    --table "0 ${LENGTH_IN_SECTORS} thin-pool ${META_DEV} ${DATA_DEV} ${DATA_BLOCK_SIZE} ${LOW_WATER_MARK}"

cat << EOF
#
# Add this to your config.toml configuration file and restart containerd daemon
#
[plugins]
  [plugins.devmapper]
    pool_name = "${POOL_NAME}"
    root_path = "${DATA_DIR}"
    base_image_size = "10GB"
    discard_blocks = true
EOF
```

Use `dmsetup` to verify that the thin-pool created successfully:
```bash
sudo dmsetup ls
devpool	(253:0)
```

Once configured and restarted `containerd`, you'll see the following output:
```
INFO[2020-03-17T20:24:45.532604888Z] loading plugin "io.containerd.snapshotter.v1.devmapper"...  type=io.containerd.snapshotter.v1
INFO[2020-03-17T20:24:45.532672738Z] initializing pool device "dev-pool"
```

Another way to setup a thin-pool is via [container-storage-setup](https://github.com/projectatomic/container-storage-setup)
tool (formerly known as `docker-storage-setup`). It is a script to configure CoW file systems like devicemapper:

```bash
#!/bin/bash
set -ex

# Block device to use for devmapper thin-pool
BLOCK_DEV=/dev/sdf
POOL_NAME=devpool
VG_NAME=containerd

# Install container-storage-setup tool
git clone https://github.com/projectatomic/container-storage-setup.git
cd container-storage-setup/
sudo make install-core
echo "Using version $(container-storage-setup -v)"

# Create configuration file
# Refer to `man container-storage-setup` to see available options
sudo tee /etc/sysconfig/docker-storage-setup <<EOF
DEVS=${BLOCK_DEV}
VG=${VG_NAME}
CONTAINER_THINPOOL=${POOL_NAME}
EOF

# Run the script
sudo container-storage-setup

cat << EOF
#
# Add this to your config.toml configuration file and restart containerd daemon
#
[plugins]
  [plugins.devmapper]
    pool_name = "${VG_NAME}-${POOL_NAME}"
    base_image_size = "10GB"
EOF
```

If successful `container-storage-setup` will output:
```
+ echo VG=containerd
+ sudo container-storage-setup
INFO: Volume group backing root filesystem could not be determined
INFO: Writing zeros to first 4MB of device /dev/xvdf
4+0 records in
4+0 records out
4194304 bytes (4.2 MB) copied, 0.0162906 s, 257 MB/s
INFO: Device node /dev/xvdf1 exists.
  Physical volume "/dev/xvdf1" successfully created.
  Volume group "containerd" successfully created
  Rounding up size to full physical extent 12.00 MiB
  Thin pool volume with chunk size 512.00 KiB can address at most 126.50 TiB of data.
  Logical volume "devpool" created.
  Logical volume containerd/devpool changed.
...
```

And `dmsetup` will produce the following output:
```bash
sudo dmsetup ls
containerd-devpool          (253:2)
containerd-devpool_tdata    (253:1)
containerd-devpool_tmeta    (253:0)
```
