# containerd Namespaces and Multi-Tenancy

containerd offers a fully namespaced API so multiple consumers can all use a single containerd instance and not conflict with one another.
Namespaces allow multi-tenancy with a single daemon, no more running nested containers to achieve this.
Consumers are able to have containers with the same names but settings that vary drastically.
System or infrastructure level containers can be hidden in a namespace while user level containers are kept in another.
Underlying image content is still shared via content addresses but image names and metadata are separate per namespace.

It is important to note that namespaces, as implemented, is an administration level construct that is not meant to be used as a security feature.
It is trivial for clients to switch namespaces.

## Who specifies the namespace?

The client specifies the namespace via the `context`.
There is a `github.com/containerd/containerd/namespaces` package that allows a user to get and set the namespace on a context.

```go
// set a namespace
ctx := namespaces.WithNamespace(context.Background(), "my-namespace")

// get the namespace
ns, ok := namespaces.Namespace(ctx)
```

Because the client calls containerd's GRPC API to interact with the daemon, all API calls require a context with a namespace set.

## How low level is the implementation?

Namespaces are passed through the containerd API to the underlying plugins providing functionality.
Plugins must be written to take namespaces into account.
Filesystem paths, IDs, or other system level resources must be namespaced for a plugin to work properly.

## How does multi-tenancy work?

Simply create a new `context` and set your application's namespace on the `context`.
Make sure you use a unique namespace for your applications that do not conflict with others.

```go
ctx := context.Background()

var (
	docker = namespaces.WithNamespace(ctx, "docker")
	vmware = namespaces.WithNamespace(ctx, "vmware")
	ecs = namespaces.WithNamespace(ctx, "aws-ecs")
	cri = namespaces.WithNamespace(ctx, "cri")
)
```

## Inspecting Namespaces

If we need to inspect containers, images, or other resources in various namespaces the `ctr` tool allows you to do this.
Simply set the `--namespace,-n` flag on `ctr` to change the namespace.

```bash
> sudo ctr -n docker tasks
> sudo ctr -n cri tasks
```

You can also use the `CONTAINERD_NAMESPACE` env var when interacting with `ctr` to set or change the default namespace.
