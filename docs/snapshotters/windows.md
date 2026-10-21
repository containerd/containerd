# Windows snapshotters

containerd ships four snapshotters for Windows hosts. Three of them store
layers for Windows containers (WCOW) and one stores layers for Linux
containers running in a utility VM (LCOW).

| Snapshotter    | Containers | Layer format                  | Host requirement                                           |
|----------------|------------|-------------------------------|------------------------------------------------------------|
| `windows`      | Windows    | Windows layer directories     | Snapshotter root on an NTFS volume                         |
| `cimfs`        | Windows    | CimFS images (`.cim` files)   | Windows Server 2022 build 20348.2031 or later, or build 26100 or later (Windows Server 2025, Windows 11 24H2) |
| `blockcim`     | Windows    | Block CIMs (one file per layer) | Block CIM support in the OS, or `cimwriter.dll` present on the host |
| `windows-lcow` | Linux      | `layer.vhd` per layer         | Snapshotter root on an NTFS volume, `runhcs.exe` in `PATH` |

`windows` is the default snapshotter on Windows. A snapshotter whose host
requirement isn't met is skipped at startup, so it won't show as `ok` in the
plugin list.

To see which snapshotters and differs are loaded, run:

```powershell
PS> ctr plugins ls | Select-String "snapshotter|differ"
```

## `windows`

The `windows` snapshotter uses the Windows layer format that the Host Compute
Service (HCS) consumes directly. Each committed snapshot is a read-only layer
directory, and each active snapshot gets a writable `sandbox.vhdx` scratch
disk on top of its parent layers.

It pairs with the `windows` differ, which is the first entry in the default
diff service order, so it works without extra configuration.

## `cimfs`

The `cimfs` snapshotter stores each read-only layer as a Composite Image File
System (CimFS) image. CimFS is a read-only filesystem built for container image
layers, and all layer images live together in a `cim-layers` directory under
the snapshotter root. Writable layers still use a `sandbox.vhdx` scratch disk,
which is created from a 10 GB template VHD.

The snapshotter needs the matching `cimfs` differ, which isn't in the default
diff service order. For client-side pulls (for example `ctr image pull
--local`, or the CRI plugin with `use_local_image_pull = true`), add it ahead of
the defaults in `config.toml`:

```toml
[plugins."io.containerd.service.v1.diff-service"]
  default = ["cimfs", "windows", "windows-lcow"]
```

Pulls through the transfer service pick the differ from the transfer
service's `unpack_config` instead. Setting `unpack_config` replaces the
default entry, so keep the `windows` entry next to the new one:

```toml
[[plugins."io.containerd.transfer.v1.local".unpack_config]]
  platform = "windows/amd64"
  snapshotter = "windows"
  differ = "windows"

[[plugins."io.containerd.transfer.v1.local".unpack_config]]
  platform = "windows/amd64"
  snapshotter = "cimfs"
  differ = "cimfs"
```

To make the CRI plugin use `cimfs`, also set:

```toml
[plugins."io.containerd.cri.v1.images"]
  snapshotter = "cimfs"
```

> **Note**
> CimFS is newer than the `windows` layer format. There are open reports of
> host crashes when many images are pulled at once with `cimfs` on Windows
> Server 2022, tracked in
> [microsoft/hcsshim#2625](https://github.com/microsoft/hcsshim/issues/2625).

## `blockcim`

The `blockcim` snapshotter stores each layer as a single block CIM file. When a
container's scratch snapshot sits on more than one layer, the snapshotter
merges those layers into one merged CIM.
It needs the `blockcim` differ, configured the same way as `cimfs` above with
`blockcim` in place of `cimfs`.

It accepts the following options:

```toml
[plugins."io.containerd.snapshotter.v1.blockcim"]
  # Verify and seal layer CIMs on close so they can't be tampered with.
  enable_layer_integrity = false

  # Append a VHD footer to layer CIMs and merged CIMs.
  append_vhd_footer = false

  # Create scratch VHDs without formatting them as NTFS, so the guest can
  # format them (for example with ReFS) under Hyper-V isolation. Scratch disks
  # are then at least 40 GB. Process-isolated containers can't run with this
  # enabled, because nothing on the host formats the scratch disk.
  unformatted_scratch = false
```

`unformatted_scratch` is usually set together with `enable_layer_integrity`.

## `windows-lcow`

The `windows-lcow` snapshotter stores layers for Linux containers that run
inside a utility VM on a Windows host. It reports the `linux` platform with the
host's architecture, and pairs with the `windows-lcow` differ, which is already
in the default diff service order. The snapshotter calls `runhcs.exe` to create
the cached `scratch.vhdx` that each container's `sandbox.vhdx` is copied from.

## Snapshot labels

The Windows snapshotters read these labels when a snapshot is prepared.

| Label | Snapshotters | Effect |
|-------|--------------|--------|
| `containerd.io/snapshot/windows/rootfs.sizebytes` | `windows`, `cimfs`, `blockcim` | Size of the container scratch disk in bytes |
| `containerd.io/snapshot/io.microsoft.container.storage.rootfs.size-gb` | all | Scratch disk size in GB. Deprecated for the WCOW snapshotters in favor of `rootfs.sizebytes`, which wins when both are set |
| `containerd.io/snapshot/io.microsoft.vm.storage.scratch` | `windows`, `cimfs`, `blockcim` | Also create a scratch disk for the utility VM, from the base layer's `UtilityVM\SystemTemplate.vhdx` |
| `containerd.io/snapshot/io.microsoft.container.storage.rootfs.location` | `windows-lcow` | Directory to keep the cached scratch VHD in |
| `containerd.io/snapshot/io.microsoft.container.storage.reuse-scratch` | `windows-lcow` | Set to `true` to share the scratch disk of another snapshot instead of creating one |
| `containerd.io/snapshot/io.microsoft.owner.key` | `windows-lcow` | Key of the snapshot whose scratch disk is shared when `reuse-scratch` is `true` |
