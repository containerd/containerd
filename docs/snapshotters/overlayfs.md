# OverlayFS Snapshotter

The `overlayfs` snapshotter is containerd's default snapshotter on Linux. It
uses the Linux kernel's `overlay` filesystem to assemble container root
filesystems from read-only image layer directories (`lowerdir`) and a writable
container layer (`upperdir` and `workdir`).

## Configuration

The `overlayfs` snapshotter is configured under
`[plugins."io.containerd.snapshotter.v1.overlayfs"]` in containerd's
`config.toml`:

```toml
version = 3

[plugins."io.containerd.snapshotter.v1.overlayfs"]
  root_path = ""
  upperdir_label = false
  sync_remove = false
  slow_chown = false
  mount_options = []
  layer_content_caches = []
```

- `root_path`: Custom root directory for the snapshotter (defaults to
  `<root>/io.containerd.snapshotter.v1.overlayfs`).
- `upperdir_label`: Attach the `containerd.io/snapshot/overlay.upperdir` label
  to snapshots recording the location of the snapshot's layer directory.
- `sync_remove`: Remove snapshot directories synchronously during `Remove`
  instead of deferring removal to the background cleanup pass.
- `slow_chown`: Allow falling back to a recursive `chown` when user-namespace ID
  remapping is requested and kernel idmapped mounts are unavailable. See
  [User Namespaces](../user-namespaces/README.md) for details.
- `mount_options`: Additional mount options passed to `overlay` mounts (for
  example, `["index=off"]`).
- `layer_content_caches`: List of pre-populated `overlayfs` snapshotter
  directories to consult before extracting image layers.

## Layer Content Cache

When pulling an OCI image, containerd downloads and extracts missing layer
archives into `<root>/io.containerd.snapshotter.v1.overlayfs/snapshots/<id>/fs`.
On nodes that boot with a pre-warmed disk image or share a read-only disk of
pre-unpacked base images, extracting the same layers again on every node wastes
network bandwidth, CPU, and disk space.

`layer_content_caches` lists directories containing pre-populated `overlayfs`
snapshots. When a layer is unpacked through containerd's unpacker (such as CRI
`PullImage`, the Transfer service, or `client.Pull` with `WithPullUnpack`, in
either sequential or parallel unpack mode), the snapshotter checks the
configured cache directories first. On a hit, it records the cached `<id>/fs`
directory and precomputed disk usage in private snapshot metadata and returns a
read-only mount (`ro`). The unpacker recognizes the staged read-only mount,
skips extracting (`Apply`) that layer, and commits the snapshot directly. Layer
blob downloads are skipped up to the first cache miss, so an image whose layers
are all in the cache downloads no layer blobs at all. (Note that
`ctr images pull --local` fetches all blobs into the content store before
unpacking on the client side and therefore bypasses the daemon's unpacker cache
short-circuit.)

Container rootfs `overlay` mounts then reference the cached `<id>/fs` directory
in their `lowerdir` stack alongside any locally extracted layers. A miss is
downloaded and extracted into the primary snapshotter root as usual, so a
partially populated cache works transparently, including with kernel idmapped
mounts for user-namespaced workloads.

```toml
version = 3

[plugins."io.containerd.snapshotter.v1.overlayfs"]
  layer_content_caches = [
    "/mnt/image-cache",
    "/var/lib/containerd-cache/io.containerd.snapshotter.v1.overlayfs",
  ]
```

Each entry in `layer_content_caches` must be an absolute path to either:

1. An `overlayfs` snapshotter root (`<dir>/metadata.db` and
   `<dir>/snapshots/<id>/fs`), or
2. A containerd root directory
   (`<dir>/io.containerd.snapshotter.v1.overlayfs/metadata.db` and
   `<dir>/io.containerd.snapshotter.v1.overlayfs/snapshots/<id>/fs`).

Directories are searched in the order configured and the first hit wins. A
directory that does not exist (or matches the snapshotter's own `root_path`) is
treated as a miss, so an empty or not-yet-populated cache will not break pulls.

Listing multiple directories is useful when caches have different lifetimes or
owners, such as a small local cache of base images baked into the VM image plus
a larger disk of workload images attached to the node.

Populating and maintaining a cache is the operator's responsibility: containerd
opens each cache's `metadata.db` in read-only mode once on first lookup and
never writes to or deletes files in cache directories, so they can reside on a
read-only mount.

> [!IMPORTANT]
> - Treat each cache directory as immutable: layers added after its
>   `metadata.db` is indexed are not picked up until `containerd` restarts (a
>   cache directory absent at startup may still be mounted later and will be
>   indexed on the next image pull). Mutating `<id>/fs` contents or replacing a
>   cache in-place at the same path with reused snapshot `<id>`s is unsupported
>   while any primary snapshot references it — even across `containerd` restarts
>   — because staged cache hits skip `Apply` digest verification and committed
>   snapshots persist `<cache>/snapshots/<id>/fs` paths in `metadata.db`. Use a
>   new versioned cache path when rotating caches, or remove affected primary
>   images and snapshots before replacing a cache at the same path and
>   restarting `containerd`.
> - A cache hit references the cached `<id>/fs` directory in-place rather than
>   copying it, so the cache must remain mounted for as long as any snapshot in
>   the primary store references it. If a cache is unmounted or an `<id>/fs`
>   entry is deleted after indexing, new pulls of uncommitted layers fall back
>   to downloading and extracting locally, whereas snapshots already committed
>   from that cache remain recorded in `metadata.db` — re-pulling the image (or
>   parallel-unpacking child layers onto it) may still succeed and then fail at
>   container rootfs creation until the cache mount is restored or the affected
>   images/snapshots are deleted and re-pulled.
> - When all layers of an image come from a single `snapshots` directory (either
>   one cache or `root_path`), `containerd` compacts `lowerdir` entries to
>   `<id>/fs` relative paths before calling `mount(2)`. When a single image's
>   layers span multiple `snapshots` directories (for example, a partial cache
>   hit mixing cached and local layers, or hits across multiple caches), only
>   their shared parent directory prefix can be stripped — and none at all when
>   they only share `/` (such as `/mnt/...` and `/var/lib/...`). Deeply layered
>   images with split layers can therefore hit the kernel's one-page mount
>   option limit (`mount options is too long`, 4 KiB on 4 KiB-page hosts) at
>   fewer layers; keeping cache mount paths short (and avoiding the extra
>   `io.containerd.snapshotter.v1.overlayfs` nesting of layout 2) leaves headroom
>   for more layers.
> - Populate the cache in the same privilege mode (`root` vs rootless /
>   `userxattr`) as the consuming containerd instance. OverlayFS encodes opaque
>   directories using `trusted.overlay.opaque` in privileged mode and
>   `user.overlay.opaque` when mounted with `userxattr`; mixing the two will not
>   honor opaque directory whiteouts across layers.

To populate a cache directory ahead of time, run a separate containerd instance
rooted at the cache directory and pull the desired images:

```bash
$ containerd --root /mnt/image-cache --state /run/containerd-cache --address /run/containerd-cache/containerd.sock &
$ ctr --address /run/containerd-cache/containerd.sock images pull $IMG
```

Stop the seed `containerd` process before mounting or serving `/mnt/image-cache`
as a `layer_content_caches` directory so its BoltDB `metadata.db` is cleanly
flushed and no longer held open for writing.

### Metrics

When `layer_content_caches` is configured, the snapshotter exports Prometheus
counters under the `containerd_overlayfs` namespace:

| Metric | Labels | Description |
| --- | --- | --- |
| `containerd_overlayfs_layer_cache_lookups_total` | `result` (`hit`, `miss`) | Layer content cache lookups during image-layer extraction. |
| `containerd_overlayfs_layer_cache_hits_total` | `cache` (`<index>:<name>`) | Cache hits broken down by the configured cache directory that served the layer (a trailing `io.containerd.snapshotter.v1.overlayfs` path component is stripped). |
| `containerd_overlayfs_layer_cache_hit_bytes_total` | _(none)_ | Bytes of unpacked layer content served from a layer content cache (from cached snapshot disk usage). |
| `containerd_overlayfs_layer_cache_errors_total` | `reason` (`unreadable`), `cache` (`<index>:<name>`) | Cache lookup errors (such as an unreadable `metadata.db` or invalid entry) that caused a cache entry or directory to be skipped. |
