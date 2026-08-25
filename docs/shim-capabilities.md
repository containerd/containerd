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
name a transform on its own, without the `/<mount-type>` suffix, so claiming
`format` applies to `format/bind` and `format/mkdir/overlay` alike, wherever
either appears in a chain. See [mounts.md](mounts.md) for what each transform
does.

containerd translates these into the `mount.WithAllowMountType` and
`mount.WithAllowTransform` [activation options][activateopts]. Attaching the
extension with neither field set means the shim handles nothing beyond
ordinary system mounts.

A claimed transform is only ever honored as a suffix of the chain it appears
in: transforms apply outside-in, so an inner one's input is an outer one's
output, and the mount manager still applies an outer, unclaimed transform even
when an inner one is claimed. In `format/mkdir/overlay`, claiming `mkdir`
gets the manager to apply `format` and hand back `mkdir/overlay`; claiming
`format` alone does nothing, since `mkdir` cannot run without it having
already run. A shim that wants to perform `format` itself should claim every
transform after it in the chain too.

`format` in particular resolves templates such as `{{ mount 0 }}` against
mount points internal to the mount manager (see [mounts.md](mounts.md)); a
shim can only claim it as part of a suffix that covers the rest of the
chain, never on its own.

Added in containerd 2.4. Replaces the deprecated
`containerd.io/runtime-allow-mounts` runtime info annotation. A shim that
does not attach this extension is still checked for that annotation as a
migration path, except for `io.containerd.runc.v2` and
`io.containerd.runhcs.v1`, which are known to never set it.

[activateopts]: mounts.md#relationship-with-runtimes
