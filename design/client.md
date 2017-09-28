# Client

Since containerd uses gRPC, it should be easy to implement 3rd-party containerd client in almost any language.
However, 3rd-party client would need to reimplement pull&push interactions with remote registries, as these are not implemented on the daemon side.

## Label conventions

Client implementations SHOULD follow this labeling convention so as to promote compatibility across different implementations.

Label                              | Object                       | Description
-----------------------------------|------------------------------|--------------------------------------------------
`containerd.io/uncompressed`       | Blob (compressed layer)      | Digest of uncompressed layer blob
`containerd.io/image.remote.ref`   | Image, Snapshot              | Image reference string such as `docker.io/library/alpine:latest`. Snapshotter MAY utilize this for lazy-pulling some blobs.

Future version MAY set `containerd.io/image.remote.resolver` explicitly as well.
Note that we never set any credential here. (That is up to snapshotter implementation.)
