# Remote Snapshotter

Containerd allows snapshotters to reuse snapshots existing somewhere managed by them.

_Remote Snapshotter_ is a snapshotter that leverages this functionality and reuses snapshots that are stored in a remotely shared place.
These remotely shared snapshots are called _remote snapshots_.
Remote snapshotter allows containerd to prepare these remote snapshots without pulling layers from registries, which hopefully shorten the time to take for image pull.

One of the remote snapshotter implementations is [Stargz Snapshotter](https://github.com/containerd/stargz-snapshotter).
This enables containerd to lazily pull images from standard-compliant registries leveraging remote snapshotter functionality and stargz images by google/crfs.

## The containerd client API

The containerd client's `Pull` API with unpacking-mode allows the underlying snapshotter to query for remote snapshots before fetching content.
Remote snapshotter needs to be plugged into containerd in [the same ways as normal snapshotters](/docs/PLUGINS.md).

```go
import (
	containerd "github.com/containerd/containerd/v2/client"
)

image, err := client.Pull(ctx, ref,
	containerd.WithPullUnpack,
	containerd.WithPullSnapshotter("my-remote-snapshotter"),
)
```

## Passing snapshotter-specific information

Some remote snapshotters requires snapshotter-specific information through `Pull` API.
The information will be used in various ways including searching snapshot contents from a remote store.
One of the example snapshotters that requires snapshotter-specific information is stargz snapshotter.
It requires the image reference name and layer digests, etc. for searching layer contents from registries.

Snapshotters receive the information through user-defined labels prefixed by `containerd.io/snapshot/`.
The containerd client supports two ways to pass these labels to the underlying snapshotter.

### Using snapshotter's `WithLabels` option

User-defined labels can be passed down to the underlying snapshotter using snapshotter option `WithLabels`.
Specified labels will be passed down every time the containerd client queries a remote snapshot.
This is useful if the values of these labels are determined statically regardless of the snapshots.
These user-defined labels must be prefixed by `containerd.io/snapshot/`.

```go
import (
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/snapshots"
)

image, err := client.Pull(ctx, ref,
	containerd.WithPullUnpack,
	containerd.WithPullSnapshotter(
		"my-remote-snapshotter",
		snapshots.WithLabels(map[string]string{
			"containerd.io/snapshot/reference": ref,
		}),
	),
)
```

### Using the containerd client's `WithImageHandlerWrapper` option

User-defined labels can also be passed using an image handler wrapper.
This is useful when labels vary depending on the snapshot.

Every time the containerd client queries remote snapshot, it passes `Annotations` appended to the targeting layer descriptor (means the layer descriptor that will be pulled and unpacked for preparing that snapshot) to the underlying snapshotter.
These annotations are passed to the snapshotter as user-defined labels.
The values of annotations can be dynamically added and modified in the handler wrapper.
Note that annotations must be prefixed by `containerd.io/snapshot/`.
`github.com/containerd/containerd/v2/pkg/snapshotters` is a handler implementation used by the CRI package, nerdctl and moby.

```go
import (
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/snapshotters"
)

if _, err := client.Pull(ctx, ref,
	containerd.WithPullUnpack,
	containerd.WithPullSnapshotter("my-remote-snapshotter"),
	containerd.WithImageHandlerWrapper(snapshotters.AppendInfoHandlerWrapper(ref)),
)
```

## Snapshotter APIs for querying remote snapshots

The containerd client queries remote snapshots to the underlying remote snapshotter using snapshotter APIs.
This section describes the high-level overview of how snapshotter APIs are used for remote snapshots functionality, with some piece of pseudo-codes that describe the simplified logic implemented in the containerd client.
For more details, see [`unpacker.go`](../pkg/unpack/unpacker.go) that implements this logic.

During image pull, the containerd client calls `Prepare` API with the label `containerd.io/snapshot.ref`.
This is a containerd-defined label which contains ChainID that targets a committed snapshot that the client is trying to prepare.
At this moment, user-defined labels (prefixed by `containerd.io/snapshot/`) will also be merged into the labels option.

```go
// Gets annotations appended to the targeting layer which would contain
// snapshotter-specific information passed by the user.
labels := snapshots.FilterInheritedLabels(desc.Annotations)
if labels == nil {
	labels = make(map[string]string)
}

// Specifies ChainID of the targeting committed snapshot.
labels["containerd.io/snapshot.ref"] = chainID

// Merges snapshotter options specified by the user which would contain
// snapshotter-specific information passed by the user.
opts := append(rCtx.SnapshotterOpts, snapshots.WithLabels(labels))

// Calls `Prepare` API with target identifier and snapshotter-specific
// information.
mounts, err = sn.Prepare(ctx, key, parent.String(), opts...)
```

If this snapshotter is a remote snapshotter, that committed snapshot hopefully exists, for example, in a shared remote store.
Remote snapshotter must define and enforce policies about whether it will use an existing snapshot.
When remote snapshotter allows the user to use that snapshot, it must return `ErrAlreadyExists`.

If the containerd client gets `ErrAlreadyExists` by `Prepare`, it ensures the existence of that committed snapshot by calling `Stat` with the ChainID.
If this snapshot is available, the containerd client skips pulling and unpacking layer that would otherwise be needed for preparing and committing that snapshot.

```go
mounts, err = sn.Prepare(ctx, key, parent.String(), opts...)
if err != nil {
	if errdefs.IsAlreadyExists(err) {
		// Ensures the layer existence
		if _, err := sn.Stat(ctx, chainID); err != nil {
			// Handling error
		} else {
			// snapshot found with ChainID
			// pulling/unpacking will be skipped
			continue
		}
	} else {
		return err
	}
}
```
