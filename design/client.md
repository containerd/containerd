# Client

Since containerd uses [gRPC](https://grpc.io/), it should be easy to implement 3rd-party containerd client in almost any language.

<!--- TODO: more information about how containerd is a client-heavy design --->

However, 3rd-party client would need to reimplement some operations, which are not implemented on the daemon side:

- Pull & Push images from remote registries, such as OCI images via Docker Registry
- Import & Export local image archives
- Generate container runtime configuration, such as OCI Runtime Spec
- Chown files under the container rootfs for remapping UID / GID

## Label conventions

Client implementations SHOULD follow this labeling convention so as to promote compatibility across different implementations.

Key (omitted prefix: `containerd.io/`)         | Object                                           | Description
-----------------------------------------------|--------------------------------------------------|--------------------------------------------------
`uncompressed`                                 | Content (compressed layer blobs), Snapshot       | Digest string of the uncompressed layer blob.
`gc.root`                                      | Content, Snapshot                                | Objects with this label are treated as the roots for GC. Clients may use nanosecond-precision RFC3339 UTC timestamp format for the value, but it is not read by the daemon.
`gc.ref.content`, `gc.ref.content.${something}`| Content                                          | Digest string of the child blob. This label represents Content->Content edges for GC.
`gc.ref.snapshot.${snapshotterName}`           | Content (image config blobs)                     | Chain ID string of layers. This label represents Content->Snapshot edges for GC.
