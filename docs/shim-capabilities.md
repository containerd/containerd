# Shim capabilities

A shim tells containerd what it is able to do by attaching typed extensions to
the `BootstrapResult` it writes at startup. See [runtime-v2.md](runtime-v2.md)
for the bootstrap protocol itself.

```proto
message BootstrapResult {
  ...
  repeated Extension extensions = 6;
}
```

containerd ignores an extension whose type it does not recognize, so a shim
can attach one unconditionally: an older containerd simply keeps its previous
behavior.

## Registry

### `containerd.types.MountCapabilities`

The shim performs some mount types or transforms itself, and the mount
manager must not perform them on its behalf.

```proto
message MountCapabilities {
  repeated string types = 1;      // e.g. "erofs", "loop"
  repeated string transforms = 2; // e.g. "format", "mkfs", "mkdir"
}
```

`types` are base mount types, with any transform prefixes removed. `transforms`
name a transform on its own, without the `/<mount-type>` suffix, so `format`
covers `format/bind` and `format/mkdir/overlay` alike. See
[mounts.md](mounts.md) for what each transform does.

containerd translates these into the `mount.WithAllowMountType` and
`mount.WithAllowTransform` [activation options][activateopts]. Attaching the
extension with neither field set means the shim handles nothing beyond
ordinary system mounts.

Added in containerd 2.4. Replaces the deprecated
`containerd.io/runtime-allow-mounts` runtime info annotation, which was
removed in the same release.

[activateopts]: mounts.md#relationship-with-runtimes
