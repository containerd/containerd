# Image Verification

The following covers the default "bindir" `ImageVerifier` plugin implementation.

To enable image verification, add a stanza like the following to the containerd config:

```yaml
[plugins]
  [plugins."io.containerd.image-verifier.v1.bindir"]
    bin_dir = "/opt/containerd/image-verifier/bin"
    max_verifiers = 10
    per_verifier_timeout = "10s"
```

All files in `bin_dir`, if it exists, must be verifier executables which conform to the following API.

## Image Verifier Binary API

### CLI Arguments

- `-name`: The given reference to the image that may be pulled.
- `-digest`: The resolved digest of the image that may be pulled.
- `-stdin-media-type`: The media type of the JSON data passed to stdin.

### Standard Input

A JSON encoded payload is passed to the verifier binary's standard input. The
media type of this payload is specified by the `-stdin-media-type` CLI
argument, and may change in future versions of containerd. Currently, the
payload has a media type of `application/vnd.oci.descriptor.v1+json` and
represents the OCI Content Descriptor of the image that may be pulled. See
[the OCI specification](https://github.com/opencontainers/image-spec/blob/main/descriptor.md)
for more details.

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
