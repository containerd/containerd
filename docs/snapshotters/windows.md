# Windows Snapshotters

containerd ships with several snapshotters for Windows container image layers.
The available snapshotter depends on the Windows version and the container
isolation mode (process isolation vs. Hyper-V isolation).

You can list the available snapshotters on your system by running:

```console
$ ctr plugins ls | grep snapshotter
```

## Core Windows snapshotters

### `windows` (legacy WCOW)

The default snapshotter for Windows containers (WCOW). It stores image layers on
an NTFS volume using WCIFS reparse points and the Host Compute Service (HCS) via
[hcsshim](https://github.com/microsoft/hcsshim). Each active snapshot gets a
`sandbox.vhdx` that serves as the writable scratch layer for the container.

**Requirements:**

- The containerd root directory must be on an NTFS volume.

**Scratch size:** The default scratch disk size can be overridden per-container
using the snapshot label `containerd.io/snapshot/windows/rootfs.sizebytes`
(value in bytes).

### `windows-lcow`

The snapshotter for Linux Containers on Windows (LCOW). It manages layers for
Linux containers running inside Hyper-V utility VMs on a Windows host. Like the
`windows` snapshotter, it requires an NTFS volume and creates `sandbox.vhdx`
files for writable layers, but uses
[runhcs](https://github.com/microsoft/hcsshim) to create and manage the scratch
VHDs.

**Requirements:**

- NTFS volume for the containerd root directory.
- Hyper-V enabled on the host.

**Scratch size:** Controlled via the label
`containerd.io/snapshot/io.microsoft.container.storage.rootfs.size-gb`
(value in GB).

**Scratch sharing:** LCOW supports sharing a single scratch disk across
multiple containers using the labels
`containerd.io/snapshot/io.microsoft.container.storage.reuse-scratch` and
`containerd.io/snapshot/io.microsoft.owner.key`.

### `cimfs`

The CimFS (Composite Image FileSystem) snapshotter uses a read-only filesystem
designed specifically for Windows container image layers. Each committed layer is
stored as a `.cim` file. CimFS layers are more space-efficient than the legacy
`windows` snapshotter because scratch VHDs are fully empty (they don't contain
WCIFS reparse points), so a single template VHD can be shared across all images.

Writable scratch layers still use `sandbox.vhdx` files, the same as the legacy
snapshotter.

**Requirements:**

- A Windows version that supports CimFS. The snapshotter checks at startup via
  `cimfs.IsCimFSSupported()` and skips registration if the host doesn't support
  it.

> **Note:** CimFS support is relatively new and may have stability issues on
> some Windows versions. There have been reports of kernel crashes (`cimfs.sys`)
> under concurrent container workloads
> (see [microsoft/hcsshim#2625](https://github.com/microsoft/hcsshim/issues/2625)).
> If you experience stability problems, use the legacy `windows` snapshotter
> instead.

### `blockcim`

The Block CIM snapshotter is the newest Windows snapshotter. It extends CimFS
with block-level CIM storage, merged CIM support, and optional data integrity
verification. When a container starts, the parent layer CIMs are merged into a
single merged CIM for faster access.

**Requirements:**

- A Windows version that supports block CIMs. The snapshotter checks at startup
  via `cimfs.IsBlockCimSupported()` and skips registration if the host doesn't
  support it.

> **Note:** The block CIM snapshotter builds on CimFS and is subject to the
> same stability considerations. See the note under [cimfs](#cimfs) above.

**Configuration:**

The `blockcim` snapshotter accepts configuration options in `config.toml`:

```toml
[plugins.'io.containerd.snapshotter.v1.blockcim']
  enable_layer_integrity = false
  append_vhd_footer = false
  unformatted_scratch = false
```

- `enable_layer_integrity`: Enable data integrity checking for CIM layers. When
  enabled, CIMs are verified and sealed on close to ensure tamper-proofing.
- `append_vhd_footer`: Append VHD footers to layer CIMs and merged CIMs.
- `unformatted_scratch`: Prepare scratch VHDs without formatting them with NTFS.
  This is useful when the scratch will be formatted with ReFS inside the guest
  (Hyper-V isolation). ReFS requires a minimum of 40 GB. Note: you cannot run
  process-isolated Windows containers with unformatted scratch because nothing on
  the host will format it.

## Choosing a snapshotter

| Snapshotter | Isolation mode | Layer format | When to use |
|-------------|---------------|-------------|-------------|
| `windows` | Process or Hyper-V | WCIFS (NTFS) | Default, widest compatibility |
| `windows-lcow` | Hyper-V (Linux guest) | VHD | Running Linux containers on Windows |
| `cimfs` | Process or Hyper-V | CIM files | Better space efficiency on supported Windows versions |
| `blockcim` | Process or Hyper-V | Block CIM files | Latest Windows builds with integrity/performance requirements |

To use a specific snapshotter, pass `--snapshotter <name>` to `ctr` or
configure it as the default in `config.toml`:

```toml
[plugins.'io.containerd.cri.v1.images']
  snapshotter = "cimfs"
```
