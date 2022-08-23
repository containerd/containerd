# Transfer Service

The transfer service is a simple flexible service which can be used to transfer artifact objects between a source and destination. The flexible API allows each implementation of the transfer interface to determines whether the transfer between the source and destination is possible. This allows new functionality to be added directly by implementations without versioning the API or requiring other implementations to handle an interface change.

The transfer service is built upon the core ideas put forth by the libchan project, that an API with binary streams and data channels as first class objects is more flexible and opens a wider variety of use cases without requiring constant protocol and API updates. To accomplish this, the transfer service makes use of the streaming service to allow binary and object streams to be accessible by transfer objects even when using grpc and ttrpc.

## Transfer Objects (Sources and Destinations)

## Transfer Operations

|   Source    | Destination | Description |
|-------------|-------------|-------------|
| Registry    | Image Store | "pull"      |
| Image Store | Registry    | "push"      |
| Object stream (Archive) | Image Store | "import" |
| Image Store | Object stream (Archive) | "export" |
| Object stream (Layer) | Mount/Snapshot | "unpack" |
| Mount/Snapshot | Object stream (Layer) | "diff" |
| Image Store | Image Store | "tag" |
| Registry | Registry | mirror registry image |

### Local containerd daemon support


## Streaming Protocols

### Progress

### Binary Streams

### Credential Passing
