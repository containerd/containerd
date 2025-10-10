# Garbage Collection

`containerd` has a garbage collector capable of removing resources which are no
longer being used. The client is responsible for ensuring that all resources
which are created are either used or held by a lease at all times, else they
will be considered eligible for removal. The Go client library
(`github.com/containerd/containerd/v2/client`) has built-in behavior to ensure resources
are properly tracked and leased. However, the lifecycles of leases are the
responsibility of the caller of the library. The `containerd` daemon has strict
resource management and will garbage collect any unused resource.

## What is a lease?

Leases are a resource in `containerd` which are created by clients and are used
to reference other resources such as snapshots and content. Leases may be
configured with an expiration or deleted by clients once it has completed an
operation. Leases are needed to inform the `containerd` daemon that a resource
may be used in the future after the client completes an operation, even though
it currently is not seen as utilized.

## How to use leases

### using Go client

The best way to use leases is to add it to a Go context immediately after the
context is created. Normally the lifespan of a lease will be the same as the
lifecycle of a Go context.

```.go
	ctx, done, err := client.WithLease(ctx)
	if err != nil {
		return err
	}
	defer done(ctx)
```

This will create a lease which will defer its own deletion and have a default
expiry of 24 hours (in case the process dies before defer). For most use cases,
this is enough and no more thought needs to be put into leases.

_But, of course, more complicated use cases are supported..._

If the program or lease are intended to be longer lived, instead of the very
easy `client.WithLease`, the lease manager can be used directly. This also
allows for setting custom labels on the lease or manipulating its resources.
Use `client.LeasesService()` to get a [lease Manager](https://godoc.org/github.com/containerd/containerd/v2/leases#Manager)
which can be used to create, list, and delete leases as well as manage the
referenced resources for that lease.

```.go
	manager := client.LeasesService()

	// this lease will never expire
	// Use `leases.WithExpiration` to make it expire
	// Use `leases.WithLabels` to apply any labels
	l, err := manager.Create(ctx, leases.WithRandomID())
	if err != nil {
		return err
	}

	// Update current context to add lease
	ctx = leases.WithLease(ctx, l.ID)

	// Do something, lease will be used...

	// Delete lease at any time, or track it to delete later
	if err := ls.Delete(ctx, l); err != nil {
		return err
	}
```


### using gRPC

The lease is not an explicit field in the API (except of course the leases
service), but rather an optional field any API service can use. Leases can
be set on any gRPC service endpoint using a gRPC header. Set the
gRPC header `containerd-lease` to the lease identifier and the API
service will operate within that lease context.

To manage the creation and deletion of leases, use the leases gRPC service.

## Garbage Collection labels

The garbage collection defines relationships between resources in two different
ways, by type specific resources properties, and by resource labels. The type
specific properties do not need to be managed by the user as they are part of
the natural structure of a resource (i.e. a container's snapshot, a snapshot's
parent, an image's target, etc). However resources may have relationships which
are not defined by `containerd`, but rather by the client. For example, an OCI
image has a manifest which references a config file and layer tars. These
resources are stored in `containerd` as generic blobs, it is the client that
understands the relationships between these blobs and sets them up using labels
on the content resources.

Resource labels can also be used to cue the garbage collector on other
properties, such as expiry, whether an object should be kept without any
reference or limit what is referenced.

The supported garbage collection labels are:

| Label key | Label value | Supported Resources | Description |
|---|---|---|---|
| `containerd.io/gc.root` | _nonempty_ | Content, Snapshots | Keep this object and anything it references. (Clients may set this to a [rfc3339](https://tools.ietf.org/html/rfc3339) timestamp to indicate when this value was set, however, the garbage collector does not parse the value) |
| `containerd.io/gc.ref.snapshot.<snapshotter>` | `<identifier>` | Content, Snapshots | Resource references the given snapshot `<identifier>` for the snapshotter `<snapshotter>` |
| `containerd.io/gc.ref.content` | _digest_ | Content, Snapshots, Images, Containers | Resource references the given content blob |
| `containerd.io/gc.ref.content.<user defined>` | _digest_ | Content, Snapshots, Images, Containers | Resource references the given content blob with a `<user defined>` label key |
| `containerd.io/gc.expire` | _timestamp_ formatted as [rfc3339](https://tools.ietf.org/html/rfc3339) | Leases | When to expire the lease. The garbage collector will delete the lease after expiration. |
| `containerd.io/gc.flat` | _nonempty_ | Leases | Ignore label references of leased resources. This only applies when the reference is originating from the lease, if the leased resources are referenced elsewhere, then their label references will be used. |
| `containerd.io/gc.bref.container` | `<identifier>` | Content, Snapshots, Images | Resource is referenced by the given container `<identifier>` |
| `containerd.io/gc.bref.content` | _digest_ | Content, Snapshots, Images | Resource is referenced by the given content blob |
| `containerd.io/gc.bref.image` | _image name_ | Content, Snapshots, Images | Resource is referenced by the given image |
| `containerd.io/gc.bref.snapshot.<snapshotter>` | `<identifier>` | Content, Snapshots, Images | Resource is referenced by the given snapshot `<identifier>` for the snapshotter `<snapshotter>` |

## Garbage Collection configuration

The garbage collector (gc) is scheduled on a background goroutine and runs based
on a number of configurable factors. By default the garbage collector will
attempt to keep the database unlocked 98% of the time based on prior
calculations of lock time from garbage collection. Also by default, the garbage
collector will not schedule itself if no deletions occurred or after every 100
database writes.

The garbage collection scheduler considers the time the database is locked
as the pause time. The garbage collection will take longer than this when
resources are being removed, for example cleaning up snapshots may be slow.
The scheduler will only schedule after the whole garbage collection is
completed but use the average pause time for determining when the next run
attempt is.

Garbage collection may be configured using the `containerd` daemon's
configuration file, usually at `/etc/containerd/config.toml`. The
configuration is under the `scheduler` plugin.

### Configuration parameters

| Configuration | Default | Description |
|---|---|---|
| `pause_threshold` | 0.02 | Represents the maximum amount of time gc should be scheduled based on the average pause time. A maximum value of .5 (50%) is enforced to prevent over scheduling. |
| `deletion_threshold` | 0 | A threshold of number of deletes to immediately trigger gc. 0 means a gc will not be triggered by deletion count, however a deletion will ensure the next scheduled gc will run. |
| `mutation_threshold` | 100 | A threshold for running gc after the given number of database mutations. Note any mutation which performed a delete will always cause gc to run, this case handles more rare events such as label reference removal. |
| `schedule_delay` | "0ms" | The delay between a trigger event and running gc. A non-zero value can be used when mutations may quickly burst. |
| `startup_delay` | "100ms" | The delay before running the initial garbage collection after daemon startup. This should be run after other startup processes have completed and no gc can be scheduled before this delay. |

The default configuration is represented as...
```.toml
version = 2
[plugins]
  [plugins."io.containerd.gc.v1.scheduler"]
    pause_threshold = 0.02
    deletion_threshold = 0
    mutation_threshold = 100
    schedule_delay = "0ms"
    startup_delay = "100ms"
```

## Synchronous Garbage Collection

In addition to garbage collections done through the scheduler, the client
may also request a garbage collection during resource removal. In this case,
the garbage collection will be scheduled immediately (or after `schedule_delay`
when configured to non-zero). The service will not return until the garbage
collection has completed. This is currently supported on removal of images and
leases. Use [`images.SynchronousDelete()`](https://godoc.org/github.com/containerd/containerd/v2/images#SynchronousDelete)
for [`images.Store`](https://godoc.org/github.com/containerd/containerd/v2/images#Store)'s
`Delete` and
[`leases.SynchronousDelete`](https://godoc.org/github.com/containerd/containerd/v2/leases#SynchronousDelete)
for [`leases.Manager`](https://godoc.org/github.com/containerd/containerd/v2/leases#Manager)'s
`Delete`.