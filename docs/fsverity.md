# Using fs-verity for Content Integrity

containerd can use the Linux kernel's `fs-verity` feature to protect on-disk image content. Support for the default local content store was added in containerd v2.0.0. When active, the kernel detects in-place tampering of content-store blobs (layers, configs, etc.) and blocks reads of corrupted data, providing defense-in-depth against [STORE-005](./security/THREAT_MODEL.md#threat-id-store-005) (at-rest blob tampering).

> [!IMPORTANT]
> `fs-verity` protects content-store **blobs at rest**. It does **not** mitigate image-unpacking attacks such as [STORE-001](./security/THREAT_MODEL.md#threat-id-store-001) (symlink / path-traversal during extraction): those writes target the snapshotter, not a content-store blob, and `fs-verity` plays no role there. It also does not defend against an attacker who can already write to containerd's directories as root (see below) — that boundary is a [Security Exclusion](./security/THREAT_MODEL.md#24-security-exclusions-non-vulnerabilities).

The default local content store in containerd opportunistically enables `fs-verity` support when the host kernel and filesystem support it. If enabling `fs-verity` on a blob fails, containerd logs a warning and continues (fail-open).

Some snapshotters (such as EROFS) support optional `fs-verity` configuration knobs (e.g., `enable_fsverity`) which can be enabled if desired.

## How fs-verity Provides Protection

`fs-verity` is best understood as protection against **at-rest corruption and offline tampering** of content-store blobs, not as a defense against a live attacker who already holds `root` (and therefore root-equivalent control of the daemon). Against an offline or non-root tampering attempt, it offers two kernel-enforced properties:

1.  **Immutability against in-place modification:** Once `fs-verity` is enabled on a file, the kernel makes its contents read-only. Any in-place write, truncate, or append fails with `EPERM` — including for `root`.
2.  **On-Demand Block Verification:** The kernel stores a Merkle tree (hash tree) for the file. When a block is read, the kernel verifies it on the fly. If the on-disk data was altered (e.g., via an offline attack or direct raw disk writes), the read fails with `EIO`, preventing use of corrupted data.

What `fs-verity` does **not** do:

*   **Replacement is still possible.** A `root` attacker can delete the protected file and write a new one with its own `fs-verity` measurement. `fs-verity` only guarantees that a file's *contents* cannot change in place after enablement. It does not protect against deletion and recreation.
*   **Signature enforcement is not used.** `fs-verity` supports signing a file's root hash and having the kernel reject unsigned files. containerd does **not** configure or enforce `fs-verity` signatures for the content store, so this property is not available as a containerd protection today.

For technical details on `fs-verity` guarantees, refer to the official [Linux Kernel Documentation](https://www.kernel.org/doc/html/latest/filesystems/fsverity.html).

## Requirements

To enable `fs-verity` support for your `containerd` installation, you must meet the following requirements:

1.  **Host Kernel Version:** Your Linux kernel must be version **5.4 or newer** and built with `CONFIG_FS_VERITY`.
2.  **Filesystem Support:** The filesystem that hosts the `containerd` root directory (e.g., `/var/lib/containerd`) must support `fs-verity`. This is independent of the snapshotter.
    *   `ext4` (Linux 5.4 and newer). Requires the `verity` feature flag on the filesystem; see below.
    *   `f2fs` (Linux 5.4 and newer). Requires the `verity` feature flag, set at `mkfs` time with `mkfs.f2fs -O verity`.
    *   `btrfs` (Linux 5.15 and newer). Requires no feature flag.

    `xfs` does not support `fs-verity`. Several distributions use it as the default root filesystem, so a `containerd` daemon running with its root directory on `xfs` cannot use `fs-verity`.

## How to Enable `fs-verity` on an `ext4` Filesystem

If you are using an `ext4` filesystem, you must enable the `verity` feature flag on the block device.

> [!NOTE]
> This is simplest to do when provisioning a new node. Enabling `verity` on an existing node's root filesystem is more disruptive: `tune2fs` requires the filesystem unmounted or read-only, so it generally means booting into a rescue/live environment (or doing it before the filesystem is first mounted).

1.  **Identify the Block Device:**
    Find the block device where your `/var/lib/containerd` (or equivalent) directory resides. You can use the `df` command:
    ```console
    $ df /var/lib/containerd
    Filesystem     1K-blocks      Used Available Use% Mounted on
    /dev/sda1      102399996  10000000  92399996  10% /
    ```
    In this example, the device is `/dev/sda1`.

2.  **Enable the `verity` feature:**
    Use `tune2fs` to enable the feature. The filesystem must be unmounted or in read-only mode to safely perform this operation. For a root filesystem, it's best to do this from a live CD or rescue environment.
    ```console
    # Ensure the filesystem is unmounted or read-only before running.
    # tune2fs -O verity /dev/sda1
    ```

3.  **Check and Repair the Filesystem:**
    Running a filesystem check immediately after modifying feature flags is highly recommended to ensure metadata consistency:
    ```console
    # e2fsck -f /dev/sda1
    ```

4.  **Verify the Feature is Enabled:**
    You can check that the feature has been enabled using `dumpe2fs`:
    ```console
    # dumpe2fs -h /dev/sda1 | grep 'Filesystem features'
    dumpe2fs 1.45.5 (07-Jan-2020)
    Filesystem features:      has_journal ext_attr resize_inode dir_index filetype needs_recovery extent 64bit flex_bg sparse_super large_file huge_file dir_nlink extra_isize metadata_csum verity
    ```
    Look for **`verity`** in the list of filesystem features.

## Verification

Once the kernel and filesystem are correctly configured, `containerd` will automatically attempt to enable `fs-verity` on new content written to the default content store. No further configuration is required for standard local storage, though optional snapshotters (like EROFS) must be configured explicitly. `containerd` logs a warning and continues if it fails to enable `fs-verity` on a supported filesystem. There is currently no explicit command to check whether a specific blob has `fs-verity` enabled through the `ctr` tool.
