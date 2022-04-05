# Stream Processors

## Processor API

Processors are a binary API that works off of content streams.

The incoming content stream will be provided to the binary via `STDIN`
and the stream processor is expected to output the processed stream on
`STDOUT`.  If errors are encountered, errors MUST be returned via `STDERR`
with a non-zero exit status.

Additional information can be provided to stream processors via a payload.
Payloads are marshaled as `protobuf.Any` types and can wrap any type of
serialized data structure.

On Unix systems, the payload, if available, is provided on `fd 3` for the process.

On Windows systems, the payload, if available, is provided via a named pipe with the
pipe's path set as the value of the environment variable `STREAM_PROCESSOR_PIPE`.

## Configuration

To configure stream processors for containerd, entries in the config file need to be made.
The `stream_processors` field is a map so that users can chain together multiple processors
to mutate content streams.

Processor Fields:

* Key - ID of the processor, used for passing a specific payload to the processor.
* `accepts` - Accepted media-types for the processor that it can handle.
* `returns` - The media-type that the processor returns.
* `path` - Path to the processor binary.
* `args` - Arguments passed to the processor binary.

```toml
version = 2

[stream_processors]
  [stream_processors."io.containerd.processor.v1.pigz"]
	accepts = ["application/vnd.docker.image.rootfs.diff.tar.gzip"]
	returns = "application/vnd.oci.image.layer.v1.tar"
	path = "unpigz"
	args = ["-d", "-c"]
```
