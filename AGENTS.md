# AGENTS.md

## Preparing changes

Before contributing, read [CONTRIBUTING.md](CONTRIBUTING.md) and the [org-wide contribution guide](https://github.com/containerd/project/blob/main/CONTRIBUTING.md). They cover change scope, tests, license headers, commit sign-offs, and AI attribution.

For AI-assisted work, follow the org guide's [Coding Agent Usage](https://github.com/containerd/project/blob/main/CONTRIBUTING.md#coding-agent-usage) policy and the repository's [submission requirements](CONTRIBUTING.md#automated-and-ai-generated-contributions). The human contributor reviews all content before submission and writes replies to review comments and issue discussions. Automated PR creation requires prior maintainer approval.

- Before adding a capability, check [SCOPE.md](SCOPE.md). Its allow-list governs features and components.
- Before adding packages or source files, read [Where to put packages](CONTRIBUTING.md#where-to-put-packages).
- Before changing a public API or protobuf definition, read [Public API Stability](RELEASES.md#public-api-stability).
- Before changing daemon configuration, read [Daemon Configuration](RELEASES.md#daemon-configuration) for compatibility and migration requirements.

When requirements leave behavior or API names ambiguous, resolve them with the human contributor before implementing the change.

## Build and validation

- **Builds and dependencies:** read [BUILDING.md](BUILDING.md) for prerequisites, binary targets, build tags, and vendoring. Regenerate `vendor/` with `make vendor`; never hand-edit vendored files.
- **Protobuf changes:** follow [Updating protobuf files](CONTRIBUTING.md#updating-protobuf-files) to regenerate code and check formatting. Never hand-edit generated protobuf code.
- **Lint:** run `make check` with the tools from [Setting up your local environment](CONTRIBUTING.md#setting-up-your-local-environment). If prerequisites are unavailable, report the blocker.
- **Tests:** use [Testing containerd](BUILDING.md#testing-containerd) to select the suite and privileges needed for the changed behavior. Check for skipped tests before reporting coverage. For CRI changes, also read [CRI Plugin Testing Guide](docs/cri/testing.md).

Run `make clean-test` only on a dedicated test host: it kills every `containerd` and `runc` process and removes runtime state.

Fix failing checks at the cause, or explain to the human contributor why the check is wrong. Never delete or weaken tests, add `//nolint`, or bypass a check just to make CI pass.

## Subsystem context

Read the relevant docs before changing a subsystem:

| Area                                      | Reference                                        |
| ----------------------------------------- | ------------------------------------------------ |
| Plugin registration and dependencies      | [Plugin model](docs/PLUGINS.md)                  |
| Task execution and shim lifecycle         | [Runtime v2](docs/runtime-v2.md)                 |
| Sandbox controllers                       | [Sandbox API](docs/sandbox-api.md)               |
| CRI requests and kubelet integration      | [CRI architecture](docs/cri/architecture.md)     |
| Content, snapshots, and their labels      | [Content flow](docs/content-flow.md)             |
| Resource retention and garbage collection | [Garbage collection](docs/garbage-collection.md) |
| Namespace propagation through context     | [Namespaces](docs/namespaces.md)                 |
| Daemon-side image transfers               | [Transfer service](docs/transfer.md)             |

## Security findings

Before scanning for security issues, evaluating a suspected vulnerability, or drafting a security report, read all three:

- [Threat model](docs/security/THREAT_MODEL.md): trust boundaries, trusted components, and security exclusions.
- [Triage guide](docs/security/TRIAGE_GUIDE.md): required evidence and finding classifications.
- [Operator baseline](docs/security/OPERATOR_GUIDELINES.md#1-baseline-security-requirements): deployment assumptions used in triage.

Apply their scope and evidence requirements before calling a finding a vulnerability. Findings outside the threat model are not vulnerabilities.

Raise suspected non-public vulnerabilities privately with the human contributor, who decides whether to use the [Security Advisories portal](https://github.com/containerd/containerd/security). Never disclose them in issues, PRs, commits, review comments, or public chat; containerd channels in the CNCF Slack are public.
