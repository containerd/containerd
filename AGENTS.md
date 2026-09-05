# AGENTS.md

Guidance for AI coding agents working in this repository. This is a discovery document: it points at the authoritative docs and adds only what they do not cover. Before starting any security scan or drafting a vulnerability report, read [Security findings and the threat model](#security-findings-and-the-threat-model).

## Build and lint

[BUILDING.md](BUILDING.md) covers build requirements, `make` targets (including single binaries and the compile-only `make build`), build tags, and vendoring. `make check` runs the linters ([CONTRIBUTING.md § Code style](CONTRIBUTING.md#code-style)) and needs the dev tools from [CONTRIBUTING.md § Setting up your local environment](CONTRIBUTING.md#setting-up-your-local-environment); install them rather than skipping the check. After changing protos in `api/`, follow [CONTRIBUTING.md § Updating protobuf files](CONTRIBUTING.md#updating-protobuf-files).

Never hand-edit `vendor/` or generated protobuf code; `make vendor` and `make protos` regenerate them.

## Tests

[BUILDING.md § Testing containerd](BUILDING.md#testing-containerd) covers the unit test, root test, and integration test targets and how to run a single test. [docs/cri/testing.md](docs/cri/testing.md) covers the CRI integration suite and its setup.

Run `make clean-test` only on a dedicated test host: it sends SIGKILL to every `containerd` and `runc` process, unmounts matching debris, and removes runtime state.

## Architecture

Two Go modules: the root `github.com/containerd/containerd/v2` and `api/` (`github.com/containerd/containerd/api`), versioned and tagged independently; a `replace` directive points the root module at the local `api/`.

[CONTRIBUTING.md § Where to put packages](CONTRIBUTING.md#where-to-put-packages) covers the top-level layout and the placement rules (no new source files in the repo root; do not add files under `test/`). Most daemon subsystems are plugins; [docs/PLUGINS.md](docs/PLUGINS.md) covers the plugin model, including how built-in plugins register.

Subsystem docs:

- [docs/runtime-v2.md](docs/runtime-v2.md): the shim lifecycle and protocol that task execution goes through.
- [docs/sandbox-api.md](docs/sandbox-api.md): the sandbox API and the two `Controller` implementations ("shim" and "podsandbox").
- [docs/cri/architecture.md](docs/cri/architecture.md): how the CRI plugin serves the kubelet.
- [docs/content-flow.md](docs/content-flow.md): content store, snapshots, and the labels that tie them together.
- [docs/garbage-collection.md](docs/garbage-collection.md): garbage collection of content and snapshots.
- [docs/namespaces.md](docs/namespaces.md): namespaces travel on `context.Context`, and nearly every operation requires one.
- [docs/transfer.md](docs/transfer.md): the daemon-side transfer service used for pulls.

## Project conventions

- Every commit needs a `Signed-off-by:` trailer (DCO, enforced in CI); the org guide's [Sign your work](https://github.com/containerd/project/blob/main/CONTRIBUTING.md#sign-your-work) covers it. That trailer and `Co-authored-by` name the humans certifying the work, never an AI assistant; `Assisted-by` is the trailer for AI assistance.
- The org-wide [containerd/project CONTRIBUTING.md](https://github.com/containerd/project/blob/main/CONTRIBUTING.md) applies to all containerd repos; read it before contributing. In particular, [new source files need a license header](https://github.com/containerd/project/blob/main/CONTRIBUTING.md#applying-license-header-to-new-files) (CI validates it via `containerd/project-checks`).
- AI policy: the org-wide guide's [Coding Agent Usage](https://github.com/containerd/project/blob/main/CONTRIBUTING.md#coding-agent-usage) section covers acceptable and unacceptable uses of AI assistance and the contributor's responsibility; [CONTRIBUTING.md § Automated and AI-generated contributions](CONTRIBUTING.md#automated-and-ai-generated-contributions) additionally requires PRs to be opened by a human, with automated PR creation approved by a maintainer first.
- [SCOPE.md](SCOPE.md) is an allow-list for features and components: anything not listed as in scope is out of scope. It governs whether a capability belongs in containerd and says nothing about ordinary bug fixes.
- API stability: [RELEASES.md § Public API Stability](RELEASES.md#public-api-stability). Breaking proto changes are rejected by CI (buf-breaking) unless the PR carries the `breaking-api-change` label.
- Daemon config is versioned and auto-migrated; see [RELEASES.md § Daemon Configuration](RELEASES.md#daemon-configuration).

## Preparing changes

A human opens and owns every pull request and writes every reply to reviews and issue discussions (see the AI policy above). Prepare changes that meet [CONTRIBUTING.md § Pull request expectations](CONTRIBUTING.md#pull-request-expectations); in addition:

- Never delete or weaken a test, add `//nolint` comments, or bypass a failing check to make CI pass; fix the cause, or tell the human you are working with why the check is wrong.
- Keep the diff minimal and focused; do not mix in refactoring, reformatting, or dependency bumps the task does not require.
- When requirements are ambiguous, ask the human you are working with instead of inventing behavior or API names.

## Security findings and the threat model

Do not characterize a finding as a vulnerability, or draft a security report, until you have read the authoritative security docs; findings outside the model they define are not vulnerabilities:

- [docs/security/THREAT_MODEL.md](docs/security/THREAT_MODEL.md): trust boundaries, security scope, the trusted computing base, and explicit security exclusions.
- [docs/security/TRIAGE_GUIDE.md](docs/security/TRIAGE_GUIDE.md): the evidence a report needs and the triage classifications for candidates that do not meet that bar.
- [docs/security/OPERATOR_GUIDELINES.md](docs/security/OPERATOR_GUIDELINES.md#1-baseline-security-requirements): Section 1 is the deployment baseline containerd assumes; reports that require violating it are Non-Vulnerabilities.

Never disclose a suspected, non-public vulnerability in an issue, PR, commit message, review comment, or public chat channel (treat the containerd channels in the CNCF Slack as public). Raise it privately with the human you are working with; they decide whether to report it through [the containerd Security Advisories portal](https://github.com/containerd/containerd/security).
