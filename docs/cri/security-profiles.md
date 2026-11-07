# OCI seccomp profiles

The CRI image service supports the alpha OCI seccomp profile flow from
[KEP-6061](https://github.com/kubernetes/enhancements/pull/6062).
`Status.features.seccomp_profile_oci` reports support on Linux. OCI AppArmor
profiles are not supported.

Pull a digest-pinned reference using `PullSecurityProfile`, with `profile_kind`
set to `Seccomp`. Registry hosts, mirrors, TLS settings, request credentials,
and configured image verifier plugins apply to the pull. The runtime handler
must be configured. Cached pulls do not contact the registry or authenticate
again; this has the same cross-workload cache implications described in the KEP.

Artifacts must be OCI image manifests with exactly one layer. The config media
type is `application/vnd.cncf.seccomp-profile.config.v1+json`, or the manifest
uses that `artifactType` with the OCI empty JSON config. The layer contains raw
OCI seccomp JSON, with no required layer media type. Documents larger than
1 MiB are rejected before decoding. The merger library's strict decoder and
artifact validator reject ambiguous JSON, notification/listener settings,
unsupported errno and argument values, incompatible rules, and profiles exceeding
the library's four artifact limits. Blob sizes and digests are verified before
publication.

Profiles live under the CRI image plugin's `security-profiles` directory and
survive daemon restart. They are separate from images and snapshots and do not
appear in `ListImages`. `ListSecurityProfiles` returns their manifest digests,
stored document sizes, and every requested reference, including cached calls.
`RemoveSecurityProfile` accepts a digest and is idempotent. Only the kubelet
controls removal; containerd does not evict profiles automatically. Removal does
not change a filter already installed in a running container.

For `RunPodSandbox` and `CreateContainer`, select `SecurityProfile.OCI` and set
`oci_ref` to the reference passed to the pull. Containerd intersects the runtime
default profile, an optional absolute-path Localhost base, and the stored OCI
profile, in that order. The Localhost base adds restrictions to the runtime
baseline. This follows the current KEP's three-input rule; older issue text and
CRI comments describe a Localhost base replacing the default. Only the merge
result is applied. OCI profiles on privileged containers, OCI fields on other
profile types, and invalid base fields are rejected with `InvalidArgument`.
Existing RuntimeDefault, Localhost, and Unconfined behavior is preserved.

When a merge constrains an artifact, a warning carries the message identifier
`SecurityProfileMergeConstrained`, profile reference, base selection, and a diff.
The runtime also exports these metrics:

- `containerd_cri_sandboxed_security_profiles`: stored profile count.
- `containerd_cri_sandboxed_security_profiles_bytes`: stored document bytes.
- `containerd_cri_security_profile_constrained_merges_total`: constrained merges.

Snapshotter ImageService proxies must forward `PullSecurityProfile`,
`ListSecurityProfiles`, and `RemoveSecurityProfile`. Those external proxies need
corresponding updates. An older proxy returns `Unimplemented`, which prevents
OCI-profile workloads from starting even when the runtime reports support.

## Validation

Unit tests cover authenticated pulls, offline cache hits, storage persistence,
reference tracking, removal, separation from images, artifact validation limits,
blob integrity, signature rejection, field validation, and merge construction.
Run the affected packages with `go test`; Linux is required for the spec tests.

`FOCUS=TestSecurityProfileOCI make cri-integration` runs a local registry, pulls
and caches an artifact through CRI, applies it to both a sandbox and a container,
checks syscall denial, removes it, and checks that the running container remains
restricted. The test needs a Linux daemon, runc, and the normal CRI integration
prerequisites. Its registry must be reachable from the daemon's network namespace.
