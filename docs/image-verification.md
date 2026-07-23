# Image Verification

The following covers the default "bindir" `ImageVerifier` plugin implementation.

To enable image verification, add a stanza like the following to the containerd config:

```yaml
[plugins]
  [plugins."io.containerd.image-verifier.v1.bindir"]
    bin_dir = "/opt/containerd/image-verifier/bin"
    max_verifiers = 10
    per_verifier_timeout = "10s"
    verify_on_run = false
```

All files in `bin_dir`, if it exists, must be verifier executables which conform to the following API.

Verification always runs on image pull. When `verify_on_run` is enabled, verification will also run at container creation (`operation=run`), and the verifier binary's standard input will include CRI request context (see [Standard Input](#standard-input)).

## Image Verifier Binary API

### CLI Arguments

- `-name`: The given reference to the image that may be pulled.
- `-digest`: The resolved digest of the image that may be pulled.
- `-stdin-media-type`: The media type of the JSON data passed to stdin.
- `-operation`: The phase invoking verification, `pull` or `run`. Only passed when `verify_on_run` is enabled.

### Standard Input

A JSON encoded payload is passed to the verifier binary's standard input. The
media type of this payload is specified by the `-stdin-media-type` CLI
argument, and may change in future versions of containerd.

The media type depends on `verify_on_run` option:

- If `verify_on_run` is disabled (default), the media type is `application/vnd.oci.descriptor.v1+json`
  - This represents the OCI Content Descriptor of the image. See [the OCI specification](https://github.com/opencontainers/image-spec/blob/main/descriptor.md) for more details.
- If `verify_on_run` is enabled, both pull and run verification use `application/vnd.containerd.image-verifier.input.v1+json`
  - This wraps the OCI Content Descriptor with the `operation` and the `annotations` containerd assigns to the container. Pull verification has no run context, so `annotations` is only populated for `run`.
  - The annotations include the [well-known CRI keys](../internal/cri/annotations/annotations.go). Some values are supplied by the runtime caller (e.g. request annotations), while others are set authoritatively by containerd (e.g. sandbox ID, container name, image name), so caller-supplied values should not be assumed trustworthy on their own.
  - The media type constant is defined in [core/images/mediatypes.go](../core/images/mediatypes.go), and its payload structure in [pkg/imageverifier/image_verifier.go](../pkg/imageverifier/image_verifier.go) (see `VerifierInput`).

### Image Pull Judgement

Print to standard output a reason for the image pull judgement.

Return an exit code of 0 to allow the image to be pulled and any other exit code to block the image from being pulled.

## Image Verifier Caller Contract

- If `bin_dir` does not exist or contains no files, the image verifier does not block image pulls.
- An image is pulled only if all verifiers that are called return an "ok" judgement (exit with status code 0). In other words, image pull judgements are combined with an `AND` operator.
- If any verifiers exceeds the `per_verifier_timeout` or fails to exec, the verification fails with an error and a `nil` judgement is returned.
- If `max_verifiers < 0`, there is no imposed limit on the number of image verifiers called.
- If `max_verifiers >= 0`, there is a limit imposed on the number of image verifiers called. The entries in `bin_dir` are lexicographically sorted by name, and the first `n = max_verifiers` of the verifiers will be called, and the rest will be skipped.
- There is no guarantee for the order of execution of verifier binaries.
- Standard error output of verifier binaries is logged at debug level by containerd, subject to truncation.
- Standard output of verifier binaries (the "reason" for the judgement) is subject to truncation.
- System resources used by verifier binaries are currently accounted for in and constrained by containerd's own cgroup, but this is subject to change.
