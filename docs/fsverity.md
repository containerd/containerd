# Using fs-verity for Content Integrity

`containerd` can leverage the Linux kernel's `fs-verity` feature to provide strong, transparent integrity and authenticity for all image content stored on disk. When enabled, the kernel will detect any on-disk tampering of content blobs (image layers, configs, etc.) and will prevent the corrupted data from being read, providing a robust defense against `STORE-001` style threats.

Enabling `fs-verity` is not a `containerd` configuration setting but rather depends on the underlying host environment. `containerd` will automatically detect and use `fs-verity` if the environment supports it.

## Requirements

To enable `fs-verity` support for your `containerd` installation, you must meet the following requirements:

1.  **Host Kernel Version:** Your Linux kernel must be version **5.4 or newer**.
2.  **Filesystem Support:** The filesystem that hosts the `containerd` root directory (e.g., `/var/lib/containerd`) must be formatted with a filesystem that supports `fs-verity`. Common choices include:
    *   `ext4`
    *   `f2fs`

## How to Enable `fs-verity` on an `ext4` Filesystem

If you are using an `ext4` filesystem, you must enable the `verity` feature flag on the block device.

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
    $ tune2fs -O verity /dev/sda1
    ```

3.  **Verify the Feature is Enabled:**
    You can check that the feature has been enabled using `dumpe2fs`:
    ```console
    $ dumpe2fs -h /dev/sda1 | grep 'Filesystem features'
    dumpe2fs 1.45.5 (07-Jan-2020)
    Filesystem features:      has_journal ext_attr resize_inode dir_index filetype needs_recovery extent 64bit flex_bg sparse_super large_file huge_file dir_nlink extra_isize metadata_csum **verity**
    ```
    Look for **`verity`** in the list of filesystem features.

## Verification

Once the kernel and filesystem are correctly configured, `containerd` will automatically detect and enable `fs-verity` on all new content written to its content store. No further configuration within `containerd` is required. `containerd` will log a warning if it fails to enable `fs-verity` on a supported filesystem, but there is currently no explicit command to check if a specific blob has `fs-verity` enabled through the `ctr` tool.
