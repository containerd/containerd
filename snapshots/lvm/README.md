## LVM block based snapshotter

This is a `containerd` snapshotter plugin which stores snapshots in `xfs` or an user preferred based filesystem format in a LVM thin pool creating sparse images.

## Requirements
LVM snapshotter requires `lvm2` set of tools which also support thin volume provisioning to be installed on the system. On ubuntu, it can be installed using `apt install lvm2 thin-provisioning-tools` command. On fedora/centos, it can be installed using `yum install lvm2` command.

## Setup

LVM snapshotter by default will format snapshots as `xfs` and depends on LVM thin provisioning tools. Make sure that the necessary components, `lvm`, `thin-provisioning-tools`(in case of Ubuntu) and `mkfs.xfs` are installed.

To make this snapshotter work you will need to prepare a `volume-group` and a `thin-volume-pool` prior to start containerd and update the configuration file, Eg:

```
[plugins]
  ...
  [plugins.lvm]
    vol_group = "vgcontainerd"
    thin_pool = "lvthincontainerd"
  ...
```

These commands are examples on how to create a volume group and a LVM thin pool. We will assume the disk is /dev/sdc

```bash
vgcreate vgcontainerd /dev/sdc
lvcreate --thinpool lvthincontainerd -l 90%FREE vgcontainerd
```

The following configuration options are honored:
* `vol_group` - a Logical volume group created using lvm tools. This is a mandatory argument.
* `thin_pool` - a Logical thin pool created using lvm tools. This is a mandatory argument.
* `root_path` - a directory where the metadata holding volume will be mounted (if empty, default location of `containerd` plugin will be used. If that does not exist `/mnt` is the final fallback).
* `img_size` - size of the thin image created within the thin pool (if empty, default size if `10G`)
* `fs_type` - filesystem type to format the image with (If empty, `xfs` filesystem will be used).


## Run
You can use this snapshotter with the below commands:

```bash
ctr images pull --snapshotter lvm docker.io/library/hello-world:latest
ctr run --snapshotter lvm docker.io/library/hello-world:latest
```
