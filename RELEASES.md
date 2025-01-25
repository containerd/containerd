# Versioning and Release

This document details the versioning and release plan for containerd. Stability
is a top goal for this project, and we hope that this document and the processes
it entails will help to achieve that. It covers the release process, versioning
numbering, backporting, API stability and support horizons.

If you rely on containerd, it would be good to spend time understanding the
areas of the API that are and are not supported and how they impact your
project in the future.

This document will be considered a living document. Supported timelines,
backport targets and API stability guarantees will be updated here as they
change.

If there is something that you require or this document leaves out, please
reach out by [filing an issue](https://github.com/containerd/containerd/issues).

## Releases

Releases of containerd will be versioned using dotted triples, similar to
[Semantic Version](http://semver.org/). For the purposes of this document, we
will refer to the respective components of this triple as
`<major>.<minor>.<patch>`. The version number may have additional information,
such as alpha, beta and release candidate qualifications. Such releases will be
considered "pre-releases".

### Major and Minor Releases

Major and minor releases of containerd will be made from main. Releases of
containerd will be marked with GPG signed tags and announced at
https://github.com/containerd/containerd/releases. The tag will be of the
format `v<major>.<minor>.<patch>` and should be made with the command `git tag
-s v<major>.<minor>.<patch>`.

After a minor release, a branch will be created, with the format
`release/<major>.<minor>` from the minor tag. All further patch releases will
be done from that branch. For example, once we release `v1.0.0`, a branch
`release/1.0` will be created from that tag. All future patch releases will be
done against that branch.

### Release Cadence

Minor releases are provided on a time basis with an initial cadence of 6 months.
The next two containerd releases should have a target release date scheduled based
on the current release cadence. Changes to the release cadence will not impact
releases which are already scheduled.

The maintainers will maintain a roadmap and milestones for each release, however,
features may be pushed to accommodate the release timeline. If your issue or feature
is not present in the roadmap, please open a Github issue or leave a
comment requesting it be added to a milestone.

### Patch Releases

Patch releases are made directly from release branches and will be done as needed
by the release branch owners.

### Pre-releases

Pre-releases, such as alphas, betas and release candidates will be conducted
from their source branch. For major and minor releases, these releases will be
done from main. For patch releases, it is uncommon to have pre-releases but
they may have an rc based on the discretion of the release branch owners.

### Support Horizon

Support horizons will be defined corresponding to a release branch, identified
by `<major>.<minor>`. Release branches will be in one of several states:

- __*Future*__: An upcoming scheduled release.
- __*Alpha*__: The next scheduled release on the main branch under active development.
- __*Beta*__: The next scheduled release on the main branch under testing. Begins 8-10 weeks before a final release.
- __*RC*__: The next scheduled release on the main branch under final testing and stabilization. Begins 2-4 weeks before a final release. For new releases where the source branch is main, the main branch will be in a feature freeze during this phase.
- __*Active*__: The release is a stable branch which is currently supported and accepting patches.
- __*Extended*__: The release branch is only accepting security patches.
- __*LTS*__: The release is a long term stable branch which is currently supported and accepting patches.
- __*End of Life*__: The release branch is no longer supported and no new patches will be accepted.

Releases will be supported at least one year after a _minor_ release. This means that
we will accept bug reports and backports to release branches until the end of
life date. If no new _minor_ release has been made, that release will be
considered supported until 6 months after the next _minor_ is released or one year,
whichever is longer. Additionally, releases may have an extended security support
period after the end of the active period to accept security backports. This
timeframe will be decided by maintainers before the end of the active status.

Long term stable (_LTS_) releases are owned by at least two maintainers for at least two
years after their initial _minor_ (x.y.0) release. The maintainers of the _LTS_ branch may commit to
a longer period or extend the support period as needed. These branches will accept bug reports and
backports until the end of life date. They may also accept a wider range of patches than non-_LTS_
releases to support the longer term maintainability of the branch, including library dependency,
toolchain (including Go) and other version updates which are needed to ensure each release is built
with fully supported dependencies. Feature backports are up to the discretion of the maintainers who
own the branch but should be rejected by default. There is no defined limit to the number of _LTS_
branches and any branch may become an _LTS_ branch after its initial release. There is no guarantee
that a new _LTS_ branch will be designated before existing _LTS_ branches reach their end of life.

### Release Owners

Every release shall be assigned owners when entering into the beta stage of the release. The initial
release owners will be responsible for creating the releases and ensuring the release is on time.
Once the release is in rc, the release owners should be part of any discussion around merging
impactful or risky changes. Every release should have at least two owners who are all active
maintainers and one of which has been a release owner in at least two prior releases.

Once the final release is out and the release branch moves to active, ownership will be
transferred back over to all committers. Active releases are maintained by all committers
until the release reaches end of life or the branch transitions to _LTS_.

Every _LTS_ release requires at least two maintainers to volunteer as owners. The owners of the
_LTS_ release may step down or be replaced by another maintainer at any time if they can no longer
support the release. If no maintainers volunteer to own the _LTS_ release after maintainers step
down, the branch will end of life after 6 months of extended support with ownership transferred back
to all committers.

### Current State of containerd Releases

| Release                                                              | Status        | Start                          | End of Life                    | Owners                 |
| -------------------------------------------------------------------- | ------------- | ------------------------------ | ------------------------------ | ---------------------- |
| [0.0](https://github.com/containerd/containerd/releases/tag/0.0.5)   | End of Life   | Dec 4, 2015                    | -                              |                        |
| [0.1](https://github.com/containerd/containerd/releases/tag/v0.1.0)  | End of Life   | Mar 21, 2016                   | -                              |                        |
| [0.2](https://github.com/containerd/containerd/tree/v0.2.x)          | End of Life   | Apr 21, 2016                   | December 5, 2017               |                        |
| [1.0](https://github.com/containerd/containerd/releases/tag/v1.0.3)  | End of Life   | December 5, 2017               | December 5, 2018               |                        |
| [1.1](https://github.com/containerd/containerd/releases/tag/v1.1.8)  | End of Life   | April 23, 2018                 | October 23, 2019               |                        |
| [1.2](https://github.com/containerd/containerd/releases/tag/v1.2.13) | End of Life   | October 24, 2018               | October 15, 2020               |                        |
| [1.3](https://github.com/containerd/containerd/releases/tag/v1.3.10) | End of Life   | September 26, 2019             | March 4, 2021                  |                        |
| [1.4](https://github.com/containerd/containerd/releases/tag/v1.4.13) | End of Life   | August 17, 2020                | March 3, 2022                  |                        |
| [1.5](https://github.com/containerd/containerd/releases/tag/v1.5.18) | End of Life   | May 3, 2021                    | February 28, 2023              |                        |
| [1.6](https://github.com/containerd/containerd/releases/tag/v1.6.0)  | LTS           | February 15, 2022              | July 23, 2025                  | @containerd/committers |
| [1.7](https://github.com/containerd/containerd/releases/tag/v1.7.0)  | LTS           | March 10, 2023                 | March 10, 2026                 | @containerd/committers |
| [2.0](https://github.com/containerd/containerd/releases/tag/v2.0.0)  | Active        | November 5, 2024               | November 7, 2025 (_tentative_) | @containerd/committers |
| [2.1](https://github.com/containerd/containerd/milestone/48)         | Alpha         | May 7, 2025 (_tentative_)      | _TBD_                          | _TBD_                  |
| [2.2](https://github.com/containerd/containerd/milestone/49)         | _Future_      | November 5, 2025 (_tentative_) | _TBD_                          | _TBD_                  |

### Kubernetes Support

The Kubernetes version matrix represents the versions of containerd which are
recommended for a Kubernetes release. Any actively supported version of
containerd may receive patches to fix bugs encountered in any version of
Kubernetes, however, our recommendation is based on which versions have been
the most thoroughly tested. See the [Kubernetes test grid](https://testgrid.k8s.io/sig-node-containerd)
for the list of actively tested versions. Kubernetes only supports n-3 minor
release versions and containerd will ensure there is always a supported version
of containerd for every supported version of Kubernetes.

| Kubernetes Version | containerd Version            | CRI Version     |
|--------------------|-------------------------------|-----------------|
| 1.29               | 1.7.11+, 1.6.27+              | v1              |
| 1.30               | 2.0.0+, 1.7.13+, 1.6.28+      | v1              |
| 1.31               | 2.0.0+, 1.7.20+, 1.6.34+      | v1              |

Deprecated containerd and kubernetes versions

| Containerd Version       | Kubernetes Version | CRI Version                          |
|--------------------------|--------------------|--------------------------------------|
| v1.0 (w/ cri-containerd) | 1.7, 1.8, 1.9      | v1alpha1                             |
| v1.1                     | 1.10+              | v1alpha2                             |
| v1.2                     | 1.10+              | v1alpha2                             |
| v1.3                     | 1.12+              | v1alpha2                             |
| v1.4                     | 1.19+              | v1alpha2                             |
| v1.5                     | 1.20+              | v1 (1.23+), v1alpha2 (until 1.25) ** |
| v1.6.15+, v1.7.0+        | 1.26+              | v1                                   |

** Note: containerd v1.6.*, and v1.7.* support CRI v1 and v1alpha2 through EOL as those releases continue to support older versions of k8s, cloud providers, and other clients using CRI v1alpha2. CRI v1alpha2 is deprecated in v1.7 and will be removed in containerd v2.0.

### Backporting

Backports in containerd are community driven. As maintainers, we'll try to
ensure that sensible bugfixes make it into _active_ release, but our main focus
will be features for the next _minor_ or _major_ release. For the most part,
this process is straightforward, and we are here to help make it as smooth as
possible.

If there are important fixes that need to be backported, please let us know in
one of three ways:

1. Open an issue.
2. Open a PR with cherry-picked change from main.
3. Open a PR with a ported fix.

__If you are reporting a security issue:__

Please follow the instructions at [containerd/project](https://github.com/containerd/project/blob/main/SECURITY.md#reporting-a-vulnerability)

Remember that backported PRs must follow the versioning guidelines from this document.

Any release that is "active" can accept backports. Opening a backport PR is
fairly straightforward. The steps differ depending on whether you are pulling
a fix from main or need to draft a new commit specific to a particular
branch.

To cherry-pick a straightforward commit from main, simply use the cherry-pick
process:

1. Pick the branch to which you want backported, usually in the format
   `release/<major>.<minor>`. The following will create a branch you can
   use to open a PR:

	```console
	$ git checkout -b my-backport-branch release/<major>.<minor>.
	```

2. Find the commit you want backported.
3. Apply it to the release branch:

	```console
	$ git cherry-pick -xsS <commit>
	```

   If all of the work from a particular PR/set of PRs is wanted,
   cherry-pick the individual commits instead of the merge commit.
   Take #8624 for example, 82ec62b is favored over 9e834e7.

   (Optional) If other commits exist in the main branch which are related
   to the cherry-picked commit; eg: fixes to the main PR. It is recommended
   to cherry-pick those commits also into this same `my-backport-branch`.
4. Push the branch and open up a PR against the _release branch_:

	```
	$ git push -u stevvooe my-backport-branch
	```

   Make sure to replace `stevvooe` with whatever fork you are using to open
   the PR. When you open the PR, make sure to switch `main` with whatever
   release branch you are targeting with the fix. Make sure the PR title has
   `[<release branch>]` prefixed. e.g.:

   ```
   [release/1.4] Fix foo in bar
   ```

If there is no existing fix in main, you should first fix the bug in main,
or ask us a maintainer or contributor to do it via an issue. Once that PR is
completed, open a PR using the process above.

Only when the bug is not seen in main and must be made for the specific
release branch should you open a PR with new code.

### Upgrade Path

The upgrade path for containerd is such that the 0.0.x patch releases are
always backward compatible with its major and minor version. Minor (0.x.0)
version will always be compatible with the previous minor release. i.e. 1.2.0
is backwards compatible with 1.1.0 and 1.1.0 is compatible with 1.0.0. There is
no compatibility guarantees for upgrades that span multiple, _minor_ releases.
For example, 1.0.0 to 1.2.0 is not supported. One should first upgrade to 1.1,
then 1.2.

There are no compatibility guarantees with upgrades to _major_ versions. For 2.0, migration was
added to ensure upgrading from 1.6 or 1.7 to 2.0 is easy. The latest releases of 1.6 and 1.7 provide
deprecation warnings if any configuration is used which is incompatible with 2.0. If deprecation
warnings are showing up, the configuration can be safely migrated in 1.6 or 1.7 before upgrading to
2.0. Once no deprecation warnings are showing up, the upgrade to 2.0 should be smooth. Always
check the release notes, breaking changes are listed there, and test your configuration before
upgrading.

## Public API Stability

The following table provides an overview of the components covered by
containerd versions:


| Component        | Status   | Stabilized Version | Links         |
|------------------|----------|--------------------|---------------|
| GRPC API         | Stable   | 1.0                | [gRPC API](#grpc-api) |
| Metrics API      | Stable   | 1.0                | - |
| Runtime Shim API | Stable   | 1.2                | - |
| Daemon Config    | Stable   | 1.0                | - |
| CRI GRPC API     | Stable   | 1.6 (_CRI v1_)     | [cri-api](https://github.com/kubernetes/cri-api/tree/master/pkg/apis/runtime/v1) |
| Go client API    | Stable   | 2.0                | [godoc](https://pkg.go.dev/github.com/containerd/containerd/v2/client) |
| `ctr` tool       | Unstable | Out of scope       | - |

From the version stated in the above table, that component must adhere to the
stability constraints expected in release versions.

Unless explicitly stated here, components that are called out as unstable or
not covered may change in a future minor version. Breaking changes to
"unstable" components will be avoided in patch versions.

Go client API stability includes the `client`, `defaults` and `version` package
as well as all packages under `pkg`, `core`, `api` and `protobuf`.
All packages under `cmd`, `contrib`, `integration`, and `internal` are not
considered part of the stable client API.

### GRPC API

The primary product of containerd is the GRPC API. As of the 1.0.0 release, the
GRPC API will not have any backwards incompatible changes without a _major_
version jump.

To ensure compatibility, we have collected the entire GRPC API symbol set into
a single file. At each _minor_ release of containerd, we will move the current
`next.pb.txt` file to a file named for the minor version, such as `1.0.pb.txt`,
enumerating the support services and messages.

Note that new services may be added in _minor_ releases. New service methods
and new fields on messages may be added if they are optional.

`*.pb.txt` files are generated at each API release. They prevent unintentional changes
to the API by having a diff that the CI can run. These files are not intended to be
consumed or used by clients.

As of containerd 2.0, the API version diverges from the main containerd version.
While containerd 2.0 is a _major_ version jump for containerd, the API will remain
on 1.x to remain backwards compatible with prior releases and existing clients.
The 2.0 release adds the API to a separate Go module which can remain as the
`github.com/containerd/containerd/api` Go package and imported separately from the
rest of containerd.

The API minor version will continue to be incremented for each major and minor
version release of containerd. However, the API is tagged directly out of the
main branch with the minor version incrementing earlier in the next release cycle
rather than at the end. This means that after the containerd 2.0 release, the next
API change is tagged as `api/v1.9.0` prior to any containerd 2.1 release. The
latest API version should be backported to all supported versions and patch
releases for prior API versions should be avoided if possible.


| Containerd Version | API Version at Release |
|--------------------|------------------------|
| v1.0               | 1.0                    |
| v1.1               | 1.1                    |
| v1.2               | 1.2                    |
| v1.3               | 1.3                    |
| v1.4               | 1.4                    |
| v1.5               | 1.5                    |
| v1.6               | 1.6                    |
| v1.7               | 1.7                    |
| v2.0               | 1.8                    |
| _v2.1_             | _1.9_                  |
| _v2.2_             | _1.10_                 |


### Metrics API

The metrics API that outputs prometheus style metrics will be versioned independently,
prefixed with the API version. i.e. `/v1/metrics`, `/v2/metrics`.

The metrics API version will be incremented when breaking changes are made to the prometheus
output. New metrics can be added to the output in a backwards compatible manner without
bumping the API version.

### Plugins API

containerd is based on a modular design where plugins are implemented to provide the core functionality.
Plugins implemented in tree are supported by the containerd community unless explicitly specified as non-stable.
Out of tree plugins are not supported by the containerd maintainers.

Currently, the Windows runtime and snapshot plugins are not stable and not supported.
Please refer to the GitHub milestones for Windows support in a future release.

#### Error Codes

Error codes will not change in a patch release, unless a missing error code
causes a blocking bug. Error codes of type "unknown" may change to more
specific types in the future. Any error code that is not "unknown" that is
currently returned by a service will not change without a _major_ release or a
new version of the service.

If you find that an error code that is required by your application is not
well-documented in the protobuf service description or tested explicitly,
please file an issue and we will clarify.

#### Opaque Fields

Unless explicitly stated, the formats of certain fields may not be covered by
this guarantee and should be treated opaquely. For example, don't rely on the
format details of a URL field unless we explicitly say that the field will
follow that format.

### Go client API

As of containerd 2.0, the Go client API documented in
[godoc](https://godoc.org/github.com/containerd/containerd/v2/client) is stable.
Note that because the Go client interfaces with the GRPC API, clients building on top
of the Go client should remain compatible with future server releases implementing the
same major GRPC API series. For backwards compatability and as a general rule of thumb,
it is the client's responsibility to handle not implemented errors returned by the containerd daemon.

Any changes to the Go client API should be detectable at compile time, so upgrading will
be a matter of fixing compilation errors and moving from there.

### CRI GRPC API

The CRI (Container Runtime Interface) GRPC API is used by a Kubernetes kubelet
to communicate with a container runtime. This interface is used to manage
container lifecycles and container images. Currently, this API is under
development and unstable across Kubernetes releases. Each Kubernetes release
only supports a single version of CRI and the CRI plugin only implements a
single version of CRI.

Each _minor_ release will support one version of CRI and at least one version
of Kubernetes. Once this API is stable, a _minor_ will be compatible with any
version of Kubernetes which supports that version of CRI.

### `ctr` tool

The `ctr` tool provides the ability to introspect and understand the containerd
API. It is not considered a primary offering of the project and is unsupported in
that sense. While we understand its value as a debug tool, it may be completely
refactored or have breaking changes in _minor_ releases.

Targeting `ctr` for feature additions reflects a misunderstanding of the containerd
architecture. Feature addition should focus on the client Go API and additions to
`ctr` may or may not be accepted at the discretion of the maintainers.

We will do our best to not break compatibility in the tool in _patch_ releases.

### Daemon Configuration

The daemon's configuration file, commonly located in `/etc/containerd/config.toml`
is versioned and backwards compatible.  The `version` field in the config
file specifies the config's version.  If no version number is specified inside
the config file then it is assumed to be a version `1` config and parsed as such.
The latest version is `version = 2`. The `main` branch is being prepared to support
the next config version `3`. The configuration is automatically migrated to the
latest version on each startup, leaving the configuration file unchanged. To avoid
the migration and optimize the daemon startup time, use `containerd config migrate`
to output the configuration as the latest version. Version `1` is no longer deprecated
and is supported by migration, however, it is recommended to use at least version `2`.

Migrating a configuration to the latest version will limit the prior versions
of containerd in which the configuration can be used. It is suggested not to
migrate your configuration file until you are confident you do not need to
quickly rollback your containerd version. Use the table of configuration
versions to containerd releases to know the minimum version of containerd for
each configuration version.

| Configuration Version | Minimum containerd version |
|-----------------------|----------------------------|
| 1                     | v1.0.0                     |
| 2                     | v1.3.0                     |
| 3                     | v2.0.0                     |

### Not Covered

As a general rule, anything not mentioned in this document is not covered by
the stability guidelines and may change in any release. Explicitly, this
pertains to this non-exhaustive list of components:

- File System layout
- Storage formats
- Snapshot formats

Between upgrades of subsequent, _minor_ versions, we may migrate these formats.
Any outside processes relying on details of these file system layouts may break
in that process. Container root file systems will be maintained on upgrade.

### Exceptions

We may make exceptions in the interest of __security patches__. If a break is
required, it will be communicated clearly and the solution will be considered
against total impact.

## Deprecated features

The deprecated features are shown in the following table:

| Component                                                                        | Deprecation release | Target release for removal            | Recommendation                           |
|----------------------------------------------------------------------------------|---------------------|---------------------------------------|------------------------------------------|
| Runtime V1 API and implementation (`io.containerd.runtime.v1.linux`)             | containerd v1.4     | containerd v2.0 ✅                    | Use `io.containerd.runc.v2`              |
| Runc V1 implementation of Runtime V2 (`io.containerd.runc.v1`)                   | containerd v1.4     | containerd v2.0 ✅                    | Use `io.containerd.runc.v2`              |
| Built-in `aufs` snapshotter                                                      | containerd v1.5     | containerd v2.0 ✅                    | Use `overlayfs` snapshotter              |
| Container label `containerd.io/restart.logpath`                                  | containerd v1.5     | containerd v2.0 ✅                    | Use `containerd.io/restart.loguri` label |
| `cri-containerd-*.tar.gz` release bundles                                        | containerd v1.6     | containerd v2.0 ✅                    | Use `containerd-*.tar.gz` bundles        |
| Pulling Schema 1 images (`application/vnd.docker.distribution.manifest.v1+prettyjws`) | containerd v1.7     | containerd v2.1 (Disabled in v2.0 ✅) | Use Schema 2 or OCI images               |
| CRI `v1alpha2`                                                                   | containerd v1.7     | containerd v2.0 ✅                    | Use CRI `v1`                             |
| Legacy CRI implementation of podsandbox support                                  | containerd v2.0     | containerd v2.0 ✅                    |                                          |
| Go-Plugin library (`*.so`) as containerd runtime plugin                          | containerd v2.0     | containerd v2.1                       | Use external plugins (proxy or binary)   |

- Pulling Schema 1 images has been disabled in containerd v2.0, but it still can be enabled by setting an environment variable `CONTAINERD_ENABLE_DEPRECATED_PULL_SCHEMA_1_IMAGE=1`
  until containerd v2.1. `ctr` users have to specify `--local` too (e.g., `ctr images pull --local`). Users of CRI clients (such as Kubernetes and `crictl`) have to specify this environment variable on the containerd daemon (usually in the systemd unit).

### Deprecated config properties
The deprecated properties in [`config.toml`](./docs/cri/config.md) are shown in the following table:

| Property Group                                                       | Property                     | Deprecation release | Target release for removal | Recommendation                                  |
|----------------------------------------------------------------------|------------------------------|---------------------|----------------------------|-------------------------------------------------|
|`[plugins."io.containerd.grpc.v1.cri"]`                               | `systemd_cgroup`             | containerd v1.3     | containerd v2.0 ✅         | Use `SystemdCgroup` in runc options (see below) |
|`[plugins."io.containerd.grpc.v1.cri".containerd]`                    | `untrusted_workload_runtime` | containerd v1.2     | containerd v2.0 ✅         | Create `untrusted` runtime in `runtimes`        |
|`[plugins."io.containerd.grpc.v1.cri".containerd]`                    | `default_runtime`            | containerd v1.3     | containerd v2.0 ✅         | Use `default_runtime_name`                      |
|`[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.*]`         | `runtime_engine`             | containerd v1.3     | containerd v2.0 ✅         | Use runtime v2                                  |
|`[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.*]`         | `runtime_root`               | containerd v1.3     | containerd v2.0 ✅         | Use `options.Root`                              |
|`[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.*]`         | `disable_cgroup`             | -                   | containerd v2.0 ✅         | Use [cgroup v2 delegation](https://rootlesscontaine.rs/getting-started/common/cgroup2/) |
|`[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.*.options]` | `CriuPath`                   | containerd v1.7     | containerd v2.0 ✅         | Set `$PATH` to the `criu` binary                |
|`[plugins."io.containerd.grpc.v1.cri".registry]`                      | `auths`                      | containerd v1.3     | containerd v2.1            | Use [`ImagePullSecrets`](https://kubernetes.io/docs/tasks/configure-pod-container/pull-image-private-registry/). See also [#8228](https://github.com/containerd/containerd/issues/8228). |
|`[plugins."io.containerd.grpc.v1.cri".registry]`                      | `configs`                    | containerd v1.5     | containerd v2.1            | Use [`config_path`](./docs/hosts.md)            |
|`[plugins."io.containerd.grpc.v1.cri".registry]`                      | `mirrors`                    | containerd v1.5     | containerd v2.1            | Use [`config_path`](./docs/hosts.md)            |
|`[plugins."io.containerd.tracing.processor.v1.otlp"]`                 | `endpoint`, `protocol`, `insecure` | containerd v1.6.29 | containerd v2.0       | Use [OTLP environment variables](https://opentelemetry.io/docs/specs/otel/protocol/exporter/), e.g. OTEL_EXPORTER_OTLP_TRACES_ENDPOINT, OTEL_EXPORTER_OTLP_PROTOCOL, OTEL_SDK_DISABLED    |
|`[plugins."io.containerd.internal.v1.tracing"]`                       | `service_name`, `sampling_ratio`   | containerd v1.6.29 | containerd v2.0       | Instead use [OTel environment variables](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/), e.g. OTEL_SERVICE_NAME, OTEL_TRACES_SAMPLER*  |


> **Note**
>
> CNI Config Template (`plugins."io.containerd.grpc.v1.cri".cni.conf_template`) was once deprecated in v1.7.0,
> but its deprecation was cancelled in v1.7.3.

<details><summary>Example: runc option <code>SystemdCgroup</code></summary><p>

```toml
version = 2

# OLD
# [plugins."io.containerd.grpc.v1.cri"]
#   systemd_cgroup = true

# NEW
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
  SystemdCgroup = true
```

</p></details>

<details><summary>Example: runc option <code>Root</code></summary><p>

```toml
version = 2

# OLD
# [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
#   runtime_root = "/path/to/runc/root"

# NEW
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
  Root = "/path/to/runc/root"
```

</p></details>

## Experimental features

Experimental features are new features added to containerd which do not have the
same stability guarantees as the rest of containerd. An effort is made to avoid
breaking interfaces between versions, but changes to experimental features before
being fully supported is possible. Users can still expect experimental features
to be high quality and are encouraged to use new features to help them stabilize
more quickly.

| Component                                                                              | Initial Release | Target Supported Release |
|----------------------------------------------------------------------------------------|-----------------|--------------------------|
| [Sandbox Service](https://github.com/containerd/containerd/pull/6703)                  | containerd v1.7 | containerd v2.0          |
| [Sandbox CRI Server](https://github.com/containerd/containerd/pull/7228)               | containerd v1.7 | containerd v2.0          |
| [Transfer Service](https://github.com/containerd/containerd/pull/7320)                 | containerd v1.7 | containerd v2.0          |
| [NRI in CRI Support](https://github.com/containerd/containerd/pull/6019)               | containerd v1.7 | containerd v2.0          |
| [gRPC Shim](https://github.com/containerd/containerd/pull/8052)                        | containerd v1.7 | containerd v2.0          |
| [CRI Runtime Specific Snapshotter](https://github.com/containerd/containerd/pull/6899) | containerd v1.7 | containerd v2.0          |
| [CRI Support for User Namespaces](./docs/user-namespaces/README.md)                    | containerd v1.7 | containerd v2.0          |
