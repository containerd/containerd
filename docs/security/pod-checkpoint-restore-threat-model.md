# Pod checkpoint and restore: threat model (DRAFT)

Status: draft for review. Intended to be folded into `THREAT_MODEL.md`
(Section 2.2 and threat CRI-005) and referenced from `docs/cri/config.md` and
`docs/sandbox-api.md` once agreed.

## 1. Scope

This document covers the CRI `CheckpointPod` and `RestorePod` RPCs, the
`sandbox.CheckpointController` capability, and the pause-container
(`podsandbox`) implementation. It does not cover the removed restore-via-
`CreateContainer` path (GHSA-p7v4-vr35-mj6f) or the standalone
`CheckpointContainer` RPC (KEP-2008), except where they share code.

## 2. Actors and trust

| Actor | Trust | Notes |
|---|---|---|
| Kubelet (CRI client) | Trusted (Class 1) | Authors `RestorePodRequest.config`, `container_configs`, `runtime_handler`, `checkpoint_path`, `output_path`. Root-equivalent per THREAT_MODEL 2.3 #1. |
| Direct CRI callers (`crictl`, other orchestrators) | Trusted | Socket access is root-equivalent; whatever path or config they pass is by design. |
| containerd, shim, runc, CRIU | Trusted (TCB) | CRIU runs as host root and executes the image's restorer blob. |
| Pod tenant (can create Pods, create `PodCheckpoint`, hold `restore` verb) | Untrusted | Controls the *process state that is dumped*. Cannot write to the checkpoint directory. |
| Node root / anyone who can write under the kubelet root | Out of scope | Already root-equivalent (THREAT_MODEL 2.4). |
| Off-node storage or transport (future backends: PVC, registry, object store) | Untrusted | Not present in alpha. |

## 3. Classification of the checkpoint directory

The existing THREAT_MODEL text classifies "the contents of container checkpoint
archives imported through CRI" as Class 2. That sentence was written for the
removed `CreateContainer` import path, where a tenant chose an *image
reference* and containerd pulled attacker-authored bytes from a registry. The
Pod-level API has a different provenance chain, and the classification must be
stated per backend:

### 3.1 NodeLocal backend (KEP-5823 alpha, the only backend today)

The directory under `checkpoint_path` is **runtime-authored node state**, not
tenant-forwarded content:

* It is created by the kubelet (mode 0700) under its own pod-checkpoints root
  and written only by containerd during `CheckpointPod` on the same node.
* The kubelet refuses cross-node restore (`PodCheckpoint.status.nodeName` must
  equal the restoring node), confines `checkpoint_path` lexically and
  canonically below its root, and enforces Pod-spec equality with the
  `checkpointedPodTemplate` (also enforced by API-server admission). The
  `restore` verb and namespace scoping decide *which* checkpoint a tenant may
  restore; they cannot decide its *contents*.
* The only principals that can alter the bytes are node root and containerd
  itself.

Therefore, for NodeLocal, containerd treats the checkpoint directory as
**Class 1-equivalent trusted state produced by its own TCB**. The parsing and
validation in `podsandbox` (bounded reads, `O_NOFOLLOW` opens on a directory
fd, strict JSON, tar entry-type and path checks, annotation allow-lists,
deterministic image names, config-digest pinning, config equality checks) is
**defense in depth against corruption, version skew, and bugs in the writer**.
It is *not* claimed to be sufficient against an adversary who can author the
directory, and Section 5 explains why no containerd-side validation can be.

The tenant still influences the checkpoint indirectly: it controls the process
state that CRIU dumps. Restoring that state under an identical Pod spec on the
same node grants nothing the tenant did not already have, provided (a) the
bytes are unmodified between dump and restore and (b) the restore-time spec is
the dump-time spec. (a) is guaranteed by node-root ownership; (b) is enforced
three times (API server, kubelet, `validateRestoreSandboxConfig` and
`validateRestoreContainerConfig`).

### 3.2 Any non-NodeLocal backend (future)

Once a checkpoint leaves the node it becomes **Class 2 content** unless its
authenticity is established cryptographically before restore. Because CRIU
restore cannot enforce destination policy (Section 5), the required control is
not sanitisation but **authentication**: containerd or CRIU must verify a
signature or MAC under a key the receiving node trusts, and must refuse to
restore anything unverified. Confidentiality (encryption of memory pages) is a
separate requirement of the same design; see Section 7.

## 4. Invariants containerd enforces (all backends)

Sinks named in THREAT_MODEL 2.2 and how the implementation closes them:

| Sink | Control | Where |
|---|---|---|
| Node-local image tags (GHSA-cvxm-645q-p574) | Archive image names are discarded; the only image record is `localhost/containerd-pod-restore:<sha256(sandboxID,containerID)>`; the base image comes from the kubelet's `container_configs[].image` and must resolve to the same config digest recorded at checkpoint. | `checkpointTargetFromIndex`, `restoreCheckpointImageName`, `validateRestoreImageConfig`, `ensureRestoreImages` |
| OCI spec (devices, mounts, hooks; GHSA-33vj-92qq-66hc) | Restored containers are created through the ordinary `CreateContainer` with the kubelet's `ContainerConfig`. Checkpoint metadata is used only for equality checks. Index annotations are allow-listed; descriptor annotations are forbidden; the task descriptor handed to the task service carries no annotations, closing the `RestoreFromPath` host-path sink. | `restorePodResources`, `validateCheckpointIndex`, `taskCheckpointDescriptor` |
| Host filesystem (GHSA-rgh6-rfwx-v388) | No path from the checkpoint is resolved on the host: log path comes from the kubelet; every read is `openat(dirfd, name, O_NOFOLLOW)` on a regular file with a size bound; the CRIU tar admits only regular files and directories with canonical relative paths; the writable layer goes through the standard diff applier. | `openCheckpointDirectory`, `openRegularFileAt`, `validateCheckpointTar`, `importContainerCheckpoint` |
| Destination security policy (GHSA-p7v4-vr35-mj6f) | Restore is refused unless the restore request's container security context, working dir, TTY, sandbox security context, sysctls and port mappings equal the recorded ones. Command, args and env may contain newly resolved Downward API values; CRIU retains the captured process state. Kubernetes enforces equality of the Pod spec, including the environment declarations. | `validateRestoreContainerConfig`, `validateRestoreSandboxConfig` |
| Denial of service | Bounded manifest, config and index sizes; 1 TiB archive cap; 100k tar entries; 1024 containers; per-sandbox and per-output-directory in-progress locks; finite context deadline required. | constants in `sandbox_checkpoint_linux.go`, `requirePodCheckpointDeadline` |

## 5. What containerd cannot enforce: the CRIU restore boundary

`runc restore` hands the CRIU image set to CRIU, which runs as host root inside
the container's namespaces and re-creates process state directly from the
images. The following are **derived from image content, not from the OCI
spec**:

* credentials (uid, gid, supplementary groups), capability sets, `no_new_privs`,
  seccomp filters, LSM labels;
* open file descriptors (reopened by the root restorer by path), sockets,
  pipes, timers, file locks;
* memory contents and register state (the restorer blob executes them);
* mount tree inside the container mount namespace (`mountpoints.img`, with
  runc mapping external mounts);
* cgroup membership and, depending on `--manage-cgroups` mode, cgroup
  properties.

containerd sees only an opaque tar of these images. The equality checks in
Section 4 compare *metadata written next to the images*; they cannot prove the
images agree with that metadata. This is the finding of GHSA-p7v4-vr35-mj6f and
the reason the old path was removed rather than patched. Consequently:

* Under Section 3.1 (NodeLocal), the invariant "images agree with recorded
  metadata" holds because both were written by containerd on this node.
* Under Section 3.2, the invariant must be supplied by authentication of the
  whole checkpoint. Without it the feature must refuse to restore.

Post-restore verification (reading `/proc/<pid>/status` of the restored tasks
and comparing `Uid`, `Gid`, `CapEff`, `CapBnd`, `NoNewPrivs`, `Seccomp` with
the spec-derived expectation, killing the task on mismatch) is a useful
defense-in-depth signal and would also fix the status-reporting gap noted in
the advisory, but it runs after CRIU has already opened files and created
mounts and cgroups, so it is not a substitute for authentication.

## 6. Residual risks and recommended follow-ups

1. **Threat-model text.** Replace the Class 2 sentence about checkpoint archives
   in THREAT_MODEL 2.2 with the per-backend statement in Section 3, and extend
   CRI-005 with the explicit "cannot enforce destination policy during CRIU
   restore" limitation.
2. **Post-restore credential verification** in `StartContainer` when
   `TaskCheckpointImage` is set (Section 5). Fail the start and remove the
   container on mismatch; report the failure in container status.
3. **Checkpoint output path TOCTOU.** `Checkpoint` validates `output_path` with
   `Lstat` and then writes through the path (`os.WriteFile`, `os.CreateTemp`,
   `os.Rename`). Hold the directory fd from validation and write with
   `openat`/`renameat` so a swapped directory cannot redirect root writes. Only
   node root can win this race, so it is out of scope by Section 2. Using
   directory-relative writes would also match the read side.
4. **Writable-layer descriptor annotations.** `validateCheckpointIndex` allows
   arbitrary annotations on the gzip layer descriptor while forbidding them
   elsewhere. Restrict to `containerd.io/uncompressed`.
5. **Residual memory in the content store.** Checkpoint images (which contain
   raw memory pages) are deleted asynchronously; blobs linger until the next
   GC. Use `images.SynchronousDelete()` for the temporary checkpoint and
   restore images. The content store root is 0700, so exposure is root-only,
   but the artefact should not outlive the operation.
6. **Default enablement.** `enable_checkpoint_restore` defaults to `true` on
   Linux, so `RestorePod` is reachable whenever CRIU is installed. The kubelet
   feature gate is off by default and direct CRI callers are root, so this is a
   policy rather than a boundary question, but the removed restore path was
   made opt-in for the same feature class; decide deliberately.
7. **Non-NodeLocal backends must ship with authentication.** Treat signature or
   MAC verification as a restore-safety requirement, not a confidentiality
   nicety, and gate any new `CheckpointSource` type on it. The `options` map on
   both RPCs is the natural carrier for key or certificate references, scoped
   by runtime handler.

## 7. Checkpoint encryption and authenticity

Research on encrypting CRIU images addresses protection of checkpoint data in
storage and transit. The proposed design assumes a trusted container engine
and runtime, with keys held in secure storage. Attackers with storage or
network access, including access obtained through a container breakout, are
in scope. Malicious containers whose state is being checkpointed are also
in scope.

The design combines ChaCha20-Poly1305 AEAD for protobuf images, a pipe-based
wrapper for raw images produced by external tools, and per-page AES-XTS for
memory. An HMAC-derived aggregate provides memory integrity while preserving
deduplication and incremental dumps. Keys are managed through X.509
certificates.

For an off-node checkpoint (Section 3.2), verification must establish that
the data came from a trusted producer and was not altered in storage or
transit before it can be treated as trusted state (Section 3.1).
Confidentiality is separate from the integrity and authenticity required
before restore. Two requirements follow for the KEP and containerd design:

* The KEP can leave encryption at rest to CRIU while still requiring
  **integrity and authenticity before restore** for future backends beyond
  NodeLocal.
* When reviewing the integrity scheme, confirm that each page is bound to
  its address or index so pages cannot be swapped or replayed within an
  authenticated image. Authentication must also cover the manifest, config
  and index together with the CRIU images.
