# containerd Threat Model

## 1. Introduction

This document provides a comprehensive threat model for the containerd container runtime. The goal of this threat model is to identify potential codebase and runtime security risks, categorize them using the STRIDE methodology, and establish qualitative risk assessments.

This threat model considers the default configuration of containerd, as well as popular extensions, plugins, and orchestrator integrations.

---

## 2. System Overview and Scope

### 2.1 Key Assets
The following assets are within the scope of containerd's protection boundary:
*   **Host Filesystem and Kernel:** The host kernel is the primary security boundary; securing host binaries and containerd daemon execution state is critical.
*   **Container Images:** Private application code, configuration assets, and registry authentication configurations.
*   **Container Runtime State:** Local metadata, container processes execution states, namespace bindings, and mounted volume boundaries.
*   **gRPC API Socket:** The local Unix socket (`/run/containerd/containerd.sock`) controlling the containerd and CRI control planes.

### 2.2 Trust Boundaries & CRI Security Expectations
The threat model analyzes boundaries crossing these six critical trust interfaces:
1.  **Host OS ↔ Container:** Kernel-enforced isolation via namespaces, cgroups, Seccomp, and AppArmor/SELinux.
2.  **Client ↔ containerd API:** Access to the local UNIX socket (`containerd.sock`), which represents a root-equivalent boundary.
3.  **containerd ↔ OCI Registry:** Network boundary for image push/pull and manifest verification.
4.  **containerd ↔ runc/shim:** Delegation of execution parameters to low-level runtime systems.
5.  **Container ↔ Container:** Lateral namespace isolation between co-located workloads.
6.  **Orchestrator (Kubernetes) ↔ containerd:** The CRI gRPC control plane trust interface.

#### CRI Security Expectations
containerd's role is to execute the container specification exactly as requested by the CRI client (e.g., Kubelet). If the Kubelet configures a workload with broad privileges (e.g., host namespaces, root execution, or privileged volume mounts), containerd will fulfill it by design. containerd's responsibility is to grant no more privilege than the client's configuration specifies, not to constrain privileges the configuration itself already permits. If the requested configuration is inherently escalation-enabling (for example, a read-write `hostPath` mount of the entire root filesystem), a workload that exercises that access is operating by design, and containerd does not prevent it.

Plugins like NRI or CNI might escalate privileges beyond what the Kubelet requested. If the operator configured the plugin to do this, it is not a vulnerability.

A security boundary bypass is only classified as a containerd vulnerability if an attacker can gain privileges *beyond* what the Kubelet explicitly requested in the container spec. Orchestrator-level misconfigurations or authorization bypasses (e.g., a tenant obtaining Kubelet-level write permissions to deploy privileged pods) reside outside containerd's security responsibility.

#### CRI Input Trust: Client vs. Forwarded Content
While the Kubelet is a trusted CRI client, not all data it sends originates with the Kubelet. CRI inputs fall into two classes, and containerd must treat them differently:

*   **Class 1 (Kubelet-authored policy):** Security context, namespaces, resource limits, masked/read-only paths, runtime handler, and mount intentions. The control plane logically vets these, and containerd executes them faithfully. If the Kubelet requests broad privileges, granting them is by design (see the CRI Security Expectations above and Section 2.4). This is what the existing CRI expectations cover.
*   **Class 2 (attacker-influenced content forwarded verbatim):** The image reference, the image bytes (layers), the values embedded in the image config (labels, environment, declared volumes, entrypoint, the `USER` directive), and the contents of container checkpoint archives imported through CRI. The Kubelet does not author or inspect this content. It forwards an image reference or a checkpoint archive import path, and containerd pulls or imports the bytes and interprets them.

containerd treats Class 2 input as untrusted content, on par with registry image bytes, not as Kubelet-authored policy. Any point where containerd re-derives a host-side runtime decision from a Class 2 value (a privileged file operation, a mount source, a device or hook injected into the OCI spec, a workload identity, a node-local image tag, or a label consumed by a containerd plugin) is a trust boundary that requires the same validation untrusted layer content already receives.

A security boundary bypass is a containerd vulnerability when attacker-influenced Class 2 content lets a tenant gain privilege, or obtain information, beyond what the Kubelet explicitly requested for that Pod, including a request for one Pod yielding state belonging to another. Misconfiguration or authorization bypass at the orchestrator (a tenant obtaining Kubelet-level write permission, or a Kubelet bug that escalates a Pod) remains Class 1 and outside containerd's responsibility. Public examples of Class 2 boundary failures include image-unpack path traversal (STORE-001), insecure image-config volume handling (CRI-002 / CVE-2022-23648), image-config values re-derived into a host sink (CRI-004), and checkpoint-metadata re-trust (CRI-005).

### 2.3. Security Scope and Expectations
containerd defines its security scope along the following six primary architectural boundaries:

#### 1. gRPC API Socket Boundary
*   **In-Scope:**
    *   Ensuring local unprivileged container workloads cannot escape container isolation to access or write to the host socket.
    *   Ensuring the daemon socket is never exposed over the network, and that internal control sockets (such as shim/runtime sockets) are not reachable across namespace boundaries (e.g., via abstract Unix sockets, as exploited in CVE-2020-15257).
*   **Out-of-Scope:** Confining, auditing, or restricting the capabilities of clients authorized to write to the `containerd.sock` UNIX socket. Because socket access is root-equivalent, actions performed by authorized socket callers are by design.

#### 2. CRI / Orchestrator Namespace Boundary
*   **In-Scope:** Enforcing that container workloads cannot gain privileges, capabilities, or host namespace access beyond what Kubelet explicitly configured in the CRI request spec.
*   **Out-of-Scope:** Enforcing lateral namespace isolation (e.g., shared network or IPC namespaces inside a Kubernetes Pod) when Kubelet explicitly requests namespace sharing.

#### 3. TCB Plugin Boundary (NRI, CNI, Snapshotters)
*   **In-Scope:** Ensuring built-in core plugins execute safely without compromising the daemon.
*   **Out-of-Scope:** Securing containerd against custom third-party plugins. All loaded plugins run inside the Trusted Computing Base (TCB); containerd does not enforce security boundaries against its own plugins.

#### 4. Workload Sandboxing Boundary
*   **In-Scope:** Safely delegating container creation and configuration down to OCI runtime shims.
*   **Out-of-Scope:** Handling container escapes that result from vulnerabilities in low-level runtimes (e.g., `runc`) or the host Linux kernel. Hard sandboxing for untrusted workloads (e.g., via Kata or gVisor) must be enforced at the orchestrator level.

#### 5. Image and Registry Resilience Boundary
*   **In-Scope:** Protecting daemon integrity and host filesystems during push, pull, and unpack operations from potentially malicious remote registries or images.
*   **Out-of-Scope:** Redacting sensitive credentials or tokens generated by external registries or third-party plugins inside error strings propagated to the orchestrator.

#### 6. Container-to-Container Lateral Boundary
*   **In-Scope:** Ensuring containerd does not itself weaken the kernel-enforced isolation (namespaces, cgroups) between co-located workloads beyond what the CRI client requested.
*   **Out-of-Scope:** Enforcing lateral isolation when the orchestrator explicitly requests shared namespaces (e.g., shared network/IPC/PID inside a Kubernetes Pod). Lateral isolation otherwise depends on the host kernel, which is a TCB dependency (see boundary #4).

### 2.4. Security Exclusions (Non-Vulnerabilities)
containerd does not consider the following classes of issues to be vulnerabilities. Exploiting these is treated as a hardening opportunity rather than a bypass of containerd security boundaries:

1.  **Secrets Leakage in Component Errors:** containerd does not control the contents of error messages generated by external or TCB components (e.g., remote registries, third-party NRI plugins). If an external component includes sensitive data (such as credentials or tokens) in its error returns, and containerd propagates this error string back to the orchestrator (Kubelet), this is not a containerd vulnerability. containerd makes a *best-effort* attempt to redact known-sensitive data in errors and logs it generates itself — for example masking userinfo and query-parameter values in registry URLs and dropping the `Authorization` header — but this coverage is **not comprehensive and offers no guarantees**: not every error path is sanitized. containerd does not attempt to filter the contents of errors that originate outside its own boundary.
2.  **Orchestrator-Authorized Privileges:** containerd executes container specifications exactly as requested by its clients, including the CRI client (Kubelet). If a client configures a Pod with broad host access (e.g., `--privileged`, host volume mounts, or shared namespaces), any host-level compromise originating from that workload is an orchestrator security configuration issue, not a containerd vulnerability.
3.  **Root/Socket Access Equivalence:** Access to the containerd Unix socket (`containerd.sock`), shim socket, or NRI socket is equivalent to root access on the host. Any exploit vector that requires direct write access to the socket or host-level root privileges to execute does not bypass a security boundary and is not classified as a vulnerability.

---

## 3. Architecture Overview

containerd is a container runtime that manages the full container lifecycle, including image transfer, storage, execution, supervision, and network attachment.

It uses a "smart client" model: the containerd daemon provides a minimal core of services, while clients handle complex tasks like building container specifications and interacting with image registries.

### 3.1 Key Components

| Component | Description |
| :--- | :--- |
| **containerd daemon** | Central process managing container lifecycle, image storage, and runtime coordination. |
| **gRPC API / Socket** | Unix socket (`/run/containerd/containerd.sock`) for client communication. |
| **CRI Plugin** | Kubernetes Container Runtime Interface implementation loaded into containerd. |
| **Content Store** | Content-addressable storage for image layers, manifests, and configurations. |
| **Metadata Store (BoltDB)** | Stores container, image, namespace, and snapshot metadata. |
| **Snapshotter** | Manages filesystem snapshots for container rootfs (overlayfs, native, devmapper, etc.). |
| **Runtime Shims** | Per-container processes bridging containerd and low-level OCI runtimes. |
| **runc (OCI runtime)** | Low-level OCI runtime executing containers via Linux namespaces and cgroups. |
| **Image Pull/Push** | Network client interacting with remote OCI registries over HTTPS. |
| **Namespace System** | Logical separation of resources within containerd. |

### 3.2 Trusted Computing Base (TCB)
The Trusted Computing Base (TCB) includes all components that are critical to the security of the system. A component is considered part of the TCB if a vulnerability or misconfiguration within it could lead to a compromise of the host system. This includes not only components that run with high privileges (e.g., as `root`), but also those that can **influence or control privileged operations**, such as by modifying OCI runtime specifications. For containerd, the TCB includes:

*   **The `containerd` daemon:** The central privileged process.
*   **Core Plugins:** All plugins compiled into `containerd`, including default snapshotters (`overlayfs`, `native`), content store, metadata store, and the CRI plugin.
*   **Runtimes & Shims:**
    *   **Low-level runtime:** Such as `runc`.
    *   **Shim:** Such as `containerd-shim-runc-v2`.
*   **External Plugins:** Configured third-party plugins interacting with containerd:
    *   **Proxy Plugins:** External gRPC services replacing a built-in service (e.g., proxy snapshotters).
    *   **gRPC Service Plugins:** External services integrating via dedicated APIs, such as **NRI (Node Resource Interface) plugins**. All loaded plugins are fully trusted and execute inside the TCB; containerd does not support or secure boundaries against "untrusted" plugins.
    *   **Executable Plugins:** External binaries executed by containerd for specific tasks (e.g., CNI plugins, Image Verifier plugins).

---

## 4. Threat Model

### 4.0 Severity and Likelihood Methodology
The **Severity** and **Likelihood** ratings in this section are qualitative guidelines for weighing the threats below; they are **not** the authoritative rating of any individual report. For a reported vulnerability, containerd maintainers and security advisors set the final severity rating (Critical / High / Moderate / Low) and coordinate disclosure during triage; confirmed vulnerabilities are published in a [GitHub Security Advisory (GHSA)](https://github.com/containerd/containerd/security). See [TRIAGE_GUIDE.md](./TRIAGE_GUIDE.md) for how reports are triaged and rated.

We evaluate security threats using a qualitative risk assessment framework based on **Severity** and **Likelihood**:

#### Severity Levels
*   **Critical:** Full host compromise, arbitrary code execution as root on the host, or complete container escape to host namespaces.
*   **High:** Access to unauthorized host directories, lateral container-to-container escape, or complete denial of service of the node's container runtime.
*   **Moderate:** Exposing local process metadata, partial registry credentials limited to the local node context, or container-level data tampering.
*   **Low:** Minor convenience bypasses, non-exploitable logs disclosures, or temporary performance degradation.

#### Likelihood Levels
*   **High:** Triggerable remotely by unprivileged users via standard orchestrator APIs (e.g., a Pod Spec or an image pull the attacker themselves controls) without requiring specialized local access.
*   **Medium:** Requires specific and uncommon configuration states, local unprivileged execution, exploiting complex timing race conditions, or inducing another party to pull/run an attacker-controlled image.
*   **Low:** Requires highly specialized local unprivileged configurations or obscure timing windows.

> [!NOTE]
> Malicious-image threats span this range. In a multi-tenant cluster, an unprivileged tenant who can reference an arbitrary image in their own Pod spec causes containerd to pull and unpack attacker-controlled content directly — this is **High**. Where the attack instead depends on inducing an *administrator or other user* to pull a poisoned image, it is **Medium**. The malicious-image threats below (STORE-001, STORE-003) are rated **Medium-High** to reflect both deployment models; treat them as High in multi-tenant environments.

---

### 4.1 Core Daemon & gRPC API

---

#### **Threat ID: CORE-001**
*   **Threat:** An unauthorized user or process gains access to the containerd gRPC socket.
*   **STRIDE Category:** Elevation of Privilege (EoP), Tampering, Information Disclosure
*   **Risk Assessment:**
    *   **Non-Vulnerability:** Access to the privileged socket (`containerd.sock`) represents a root-equivalent security boundary. Any exploit requiring direct socket access does not bypass a security boundary and is classified as a Security Exclusion (Section 2.4).
*   **Mitigation:** Restrict socket access. See [OPERATOR_GUIDELINES.md (permissions)](./OPERATOR_GUIDELINES.md#13-permissions-constraints) for baseline socket configurations.

> [!NOTE]
> **Repudiation Note:** containerd does not collect fine-grained, high-fidelity audit logs that trace specific gRPC API requests back to individual host-level users. However, because socket access represents a root-equivalent security boundary, any attacker gaining access can easily manipulate host-level logs, making non-repudiation at the runtime layer secondary to securing the socket itself.

---

#### **Threat ID: CORE-002**
*   **Threat:** A vulnerability in the containerd daemon's gRPC request handling logic leads to a crash or resource exhaustion.
*   **STRIDE Category:** Denial of Service (DoS)
*   **Risk Assessment:**
    *   **Severity:** High - Crashes containerd, preventing all container management operations across the host node.
    *   **Likelihood:** Medium - Many request-handling endpoints require direct socket access (root-equivalent), but the image **pull and unpack** paths can be reached transitively when an unprivileged tenant references a malicious remote image in a standard Pod spec. Note that the OCI image *import* path (e.g., CVE-2023-25153 below) is a direct socket-client operation (`ctr image import` / the transfer service) and is **not** reachable through CRI/Pod specs; its DoS therefore requires socket access.
*   **Mitigation:** Strict input validation, gRPC payload limits, and execution under systemd process supervisors that automatically restart the daemon. See [OPERATOR_GUIDELINES.md (integrity checks)](./OPERATOR_GUIDELINES.md#tier-2--recommended--advanced-hardening) for image signature verification.
*   **Real-world Examples:**
    *   **[GHSA-259w-8hf6-59c2](https://github.com/containerd/containerd/security/advisories/GHSA-259w-8hf6-59c2) ([CVE-2023-25153](https://github.com/containerd/containerd/security/advisories/GHSA-259w-8hf6-59c2)):** Lack of memory limits during OCI image import could lead to memory exhaustion (DoS).

---

#### **Threat ID: CORE-003**
*   **Threat:** An attacker with local host filesystem access or control over the daemon tampers with the containerd metadata store (boltDB).
*   **STRIDE Category:** Denial of Service (DoS), Tampering
*   **Risk Assessment:**
    *   **Non-Vulnerability:** Requires direct write access to `/var/lib/containerd`, which is restricted to root and classified as a Security Exclusion (Section 2.4).
*   **Mitigation:** Protect data directories. See [OPERATOR_GUIDELINES.md (permissions)](./OPERATOR_GUIDELINES.md#13-permissions-constraints) for baseline directory permission rules.

---

#### **Threat ID: CORE-004**
*   **Threat:** An attacker with socket access floods the API with calls to generate an overwhelming volume of event messages, exhausting daemon resources.
*   **STRIDE Category:** Denial of Service (DoS)
*   **Risk Assessment:**
    *   **Non-Vulnerability:** Requires direct gRPC socket connection and is classified as a Security Exclusion (Section 2.4).
*   **Mitigation:** Connection rate limiting and event queue resource controls.

*Note: While general gRPC endpoints are susceptible to DoS from rapid requests (addressed broadly in `CORE-002`), the events subsystem is uniquely vulnerable due to its pub/sub broadcast design, which amplifies serialization overhead across multiple concurrent subscribers.*

---

#### **Threat ID: CORE-005**
*   **Threat:** An attacker modifies the containerd configuration file (`config.toml`) to disable security features or inject malicious plugins.
*   **STRIDE Category:** Tampering, Elevation of Privilege (EoP)
*   **Risk Assessment:**
    *   **Non-Vulnerability:** Requires write permissions to `/etc/containerd/config.toml` (restricted to root) and is classified as a Security Exclusion (Section 2.4).
*   **Mitigation:** Protect configuration files with strict root-owned permissions. See [OPERATOR_GUIDELINES.md (permissions)](./OPERATOR_GUIDELINES.md#13-permissions-constraints) for baseline permissions, and [OPERATOR_GUIDELINES.md (FIM)](./OPERATOR_GUIDELINES.md#tier-3--defense-in-depth) for file integrity auditing.

---

#### **Threat ID: CORE-006**
*   **Threat:** Overly permissive permissions on containerd's runtime or metadata directories allow local unprivileged traversal.
*   **STRIDE Category:** Information Disclosure, Elevation of Privilege (EoP)
*   **Risk Assessment:**
    *   **Severity:** High - Traversal of internal directories exposes sensitive volume paths, metadata, and credentials. Access to setuid binaries from mounted volumes can lead to host privilege escalation.
    *   **Likelihood:** Medium - Occurred historically via default configurations before directory permission tightening.
*   **Mitigation:** Apply strict directory structures. See [OPERATOR_GUIDELINES.md (permissions)](./OPERATOR_GUIDELINES.md#13-permissions-constraints) for baseline directory permission settings.
*   **Real-world Examples:**
    *   **[CVE-2024-25621](https://nvd.nist.gov/vuln/detail/CVE-2024-25621):** The daemon root (`/var/lib/containerd`) and per-plugin state directories (the CRI and shim state directories under `/run/containerd`) were created group- and world-traversable, so local users could reach internal paths and read sensitive assets.

---

### 4.2 Runtime and Shims

---

#### **Threat ID: RUN-001**
*   **Threat:** A vulnerability in the low-level runtime (`runc`) allows a malicious process in a container to escape to the host.
*   **STRIDE Category:** Elevation of Privilege (EoP)
*   **Risk Assessment:**
    *   **Scope:** `runc` and the host kernel are **TCB dependencies** (see Section 2.3, boundary #4, and TCB-001). A flaw originating *within* `runc` or the kernel is owned and fixed upstream, not by containerd; such reports are referred upstream during triage. This threat is documented here because containerd's job is to delegate execution safely and operators must keep these components patched — not because runc-internal escapes are within containerd's own security boundary.
    *   **Severity:** Critical - Full host compromise, escaping container isolation to the host namespaces.
    *   **Likelihood:** Medium - Exploiting a kernel or runc vulnerability is highly complex, but escapes are a recurring vulnerability class and public exploits are highly stable once a flaw is disclosed.
*   **Mitigation:** Keep `runc` and the host kernel fully patched. Enforce default profiles where supported. See [OPERATOR_GUIDELINES.md (patching)](./OPERATOR_GUIDELINES.md#12-dependency-patching-runc) and [OPERATOR_GUIDELINES.md (profiles)](./OPERATOR_GUIDELINES.md#tier-1--highly-recommended--basic-hardening).
*   **Real-world Examples:**
    *   **runc Escape CVEs:** [CVE-2024-21626](https://nvd.nist.gov/vuln/detail/CVE-2024-21626) (leaked file descriptor / `WORKDIR`), and [CVE-2025-31133](https://nvd.nist.gov/vuln/detail/CVE-2025-31133) / [CVE-2025-52565](https://nvd.nist.gov/vuln/detail/CVE-2025-52565) / [CVE-2025-52881](https://nvd.nist.gov/vuln/detail/CVE-2025-52881) (escapes via runtime mount operations).

---

#### **Threat ID: RUN-002**
*   **Threat:** An attacker replaces the `runc` binary on the host with a malicious version.
*   **STRIDE Category:** Elevation of Privilege (EoP), Tampering
*   **Risk Assessment:**
    *   **Non-Vulnerability:** Requires host write permissions to the system binary paths (restricted to root) and is classified as a Security Exclusion (Section 2.4).
*   **Mitigation:** Enforce strict read-only permissions on host binary paths. See [OPERATOR_GUIDELINES.md (permissions)](./OPERATOR_GUIDELINES.md#13-permissions-constraints) for baseline file settings, and [OPERATOR_GUIDELINES.md (FIM)](./OPERATOR_GUIDELINES.md#tier-3--defense-in-depth) for file integrity tracking.

---

#### **Threat ID: RUN-003**
*   **Threat:** A misconfigured custom OCI runtime allows for a container escape.
*   **STRIDE Category:** Elevation of Privilege (EoP)
*   **Risk Assessment:**
    *   **Severity:** Critical - Full host compromise.
    *   **Likelihood:** Low-Medium - Requires specific custom wrapper configurations or script designs.
*   **Common Misconfiguration Scenarios:**
    *   **Command Injection in Wrapper Scripts:** A custom runtime implemented as a shell wrapper script that parses parameters in the OCI `config.json` spec but executes host-level command lines insecurely, allowing container users to trigger host execution.
    *   **Over-privileged Directory Sharing in VM Shims:** Virtual-machine-based shims (e.g., Kata, gVisor) configured to share highly sensitive host directories (such as the host `/` or `/var/run`) with write privileges via `virtio-fs` or 9p.
    *   **Disabled Default Security Enforcements:** Custom runtimes that silently drop or misapply Linux capabilities, Seccomp filters, or AppArmor/SELinux profiles supplied by containerd.
*   **Mitigation:** Auditing and secure-coding reviews of custom wrappers. See [OPERATOR_GUIDELINES.md (sandboxing)](./OPERATOR_GUIDELINES.md#tier-3--defense-in-depth) for sandboxed VM runtimes.

---

#### **Threat ID: RUN-004**
*   **Threat:** A container process exhausts host resources (CPU, memory, PIDs) due to missing cgroup limits.
*   **STRIDE Category:** Denial of Service (DoS)
*   **Risk Assessment:**
    *   **Severity:** High - Starves surrounding containers and host processes, rendering the node unresponsive.
    *   **Likelihood:** High - Zero special privileges or exploits required; easily triggered by standard application code or simple loops if limits are missing.
*   **Mitigation:** Always enforce cgroup CPU, memory, and PID limits on all workloads to prevent resource DoS.

---

#### **Threat ID: RUN-005**
*   **Threat:** A container reaches the shim's control socket (or other shim IPC surface) across a namespace boundary and uses it to spawn processes or otherwise influence execution, escalating privileges toward the host.
*   **STRIDE Category:** Elevation of Privilege (EoP)
*   **Risk Assessment:**
    *   **Severity:** Moderate - Reaching the shim's control API can lead to privilege escalation rather than merely a crash or disclosure, but the precondition (a workload sharing the host network namespace) constrains exposure. containerd rated the historical instance (CVE-2020-15257) **Moderate**.
    *   **Likelihood:** Low-Medium - The historical vector required a `hostNetwork` workload. Current shims close this by default (see mitigation), so exploitation now requires a new flaw in shim socket handling.
*   **Mitigation:** Keep shims updated. Shims now use path-based Unix sockets with filesystem-based ACLs (mode `0600`) instead of abstract Unix sockets, preventing cross-namespace reachability from `hostNetwork` workloads.
*   **Real-world Examples:**
    *   **[GHSA-36xw-fx78-c5r4](https://github.com/containerd/containerd/security/advisories/GHSA-36xw-fx78-c5r4) ([CVE-2020-15257](https://nvd.nist.gov/vuln/detail/CVE-2020-15257)):** The containerd-shim API was exposed over an abstract Unix socket guarded only by an effective-UID-0 check. A container sharing the host network namespace could reach the shim API and cause new processes to run with elevated privileges. Rated Moderate by containerd; resolved by the move to path-based sockets.

---

#### **Threat ID: RUN-006**
*   **Threat:** Interactive process execution (`exec`) is triggered inside a container without producing a forensics trail.
*   **STRIDE Category:** Repudiation
*   **Risk Assessment:**
    *   **Severity:** Moderate - Hides malicious post-compromise actions (data exfiltration, backdoor installation) from audit logs.
    *   **Likelihood:** High - containerd's core API does not log execution commands once the process handles are handed to the shim.
*   **Mitigation:** Deploy eBPF-based runtime tracing. See [OPERATOR_GUIDELINES.md (observability)](./OPERATOR_GUIDELINES.md#tier-2--recommended--advanced-hardening) for real-time system call auditing.

---

#### **Threat ID: RUN-007**
*   **Threat:** Container environment variables containing sensitive secrets leak into host logs or filesystem nodes.
*   **STRIDE Category:** Information Disclosure
*   **Risk Assessment:**
    *   **Severity:** High - Exposure of critical database credentials, TLS keys, or API credentials.
    *   **Likelihood:** High - Environment variables are highly visible inside `/proc/[pid]/environ` on the host, in metadata database stores, and in standard container logs.
*   **Mitigation:** Avoid passing secrets as environment variables. Use volume-mounted secrets. See [OPERATOR_GUIDELINES.md (secrets protection)](./OPERATOR_GUIDELINES.md#tier-2--recommended--advanced-hardening).

---

### 4.3 Storage (Snapshotters and Content Store)

---

#### **Threat ID: STORE-001**
*   **Threat:** A maliciously crafted image abuses symlinks or path traversal during unpacking to write or modify files at arbitrary host locations outside the snapshot root.
*   **STRIDE Category:** Tampering, Elevation of Privilege (EoP)
*   **Risk Assessment:**
    *   **Severity:** Critical - Arbitrary file write or modification on the host filesystem, leading to full host compromise.
    *   **Likelihood:** Medium-High - The attacker only needs containerd to pull and unpack their image (see the malicious-image note in Section 4.0; High in multi-tenant clusters).
*   **Mitigation:** Keep containerd updated — unpacking path-traversal and symlink handling are fixed in the daemon itself, which is the primary control. Because the daemon performs extraction as root, directory permissions do **not** prevent this traversal; defense relies on the unpacker's own boundary checks. Image signature verification (rejecting untrusted images before unpack) reduces exposure. See [OPERATOR_GUIDELINES.md (image integrity)](./OPERATOR_GUIDELINES.md#tier-2--recommended--advanced-hardening). **Note:** `fs-verity` does *not* mitigate this threat — it protects content-store blobs at rest (see STORE-005), but the write occurs in the snapshotter during extraction, not against a content-store blob.
*   **Real-world Examples:**
    *   **[GHSA-cm76-qm8v-3j95](https://github.com/containerd/containerd/security/advisories/GHSA-cm76-qm8v-3j95) ([CVE-2025-47290](https://github.com/containerd/containerd/security/advisories/GHSA-cm76-qm8v-3j95)):** A symlink TOCTOU race during image unpacking allowed writing through a symlink to arbitrary host locations. This was a regression specific to containerd **2.1.0** (fixed in 2.1.1), not a flaw in the long-standing core unpacker.
    *   **[GHSA-c72p-9xmj-rx3w](https://github.com/containerd/containerd/security/advisories/GHSA-c72p-9xmj-rx3w) ([CVE-2021-32760](https://github.com/containerd/containerd/security/advisories/GHSA-c72p-9xmj-rx3w)):** Overly permissive archive handling allowed `chmod` operations outside the target extraction path.

---

#### **Threat ID: STORE-002**
*   **Threat:** An attacker exhausts host disk space by pulling extremely large images (storage exhaustion DoS).
*   **STRIDE Category:** Denial of Service (DoS)
*   **Risk Assessment:**
    *   **Severity:** Moderate - Fills disk space, halting new container deployments and causing host instability.
    *   **Likelihood:** Medium - Standard unprivileged users can transitively trigger large image downloads via orchestrator APIs.
*   **Mitigation:** Enforce strict disk space limits and storage quotas on the content store and snapshot storage (where layer blobs and unpacked rootfs data accumulate), not merely the BoltDB metadata. See [OPERATOR_GUIDELINES.md (disk quotas)](./OPERATOR_GUIDELINES.md#tier-2--recommended--advanced-hardening).

---

#### **Threat ID: STORE-003**
*   **Threat:** A maliciously crafted image exploits parsing or extraction flaws in the content store's unpacking pipeline. This covers parsers running inside containerd; for layer content parsed by the kernel instead, see STORE-006.
*   **STRIDE Category:** Denial of Service (DoS), Elevation of Privilege (EoP)
*   **Susceptible Components:**
    *   **JSON/Schema Parsers:** Ambiguity or resource limits in parsing OCI image manifests, configuration files, and index JSON documents.
    *   **Decompression Libraries:** Vulnerabilities (or resource exhaustion zip/tar bombs) during the decompression of compressed layer blobs (e.g., `gzip`, `zstd`).
    *   **Tar Unpacking & Archive Handling:** Flaws in the extraction of tar files to snapshotter directories, including path traversal or symlink manipulation.
*   **Risk Assessment:**
    *   **Severity:** High - The realistic impact of this class is resource-exhaustion DoS of the privileged daemon (decompression bombs, pathological parsers). Worst-case memory-safety or extraction flaws could in principle reach arbitrary file write or code execution, but no such containerd defect has been demonstrated; the arbitrary-write cases observed in practice are the unpacking path-traversal bugs tracked under STORE-001.
    *   **Likelihood:** Medium-High - The attacker only needs containerd to pull and unpack their image (see the malicious-image note in Section 4.0; High in multi-tenant clusters).
*   **Mitigation:** Keep containerd fully updated. Verify signatures and scan SBOMs. See [OPERATOR_GUIDELINES.md (image integrity)](./OPERATOR_GUIDELINES.md#tier-2--recommended--advanced-hardening).
*   **Real-world Examples:**
    *   **[GHSA-5j5w-g665-5m35](https://github.com/containerd/containerd/security/advisories/GHSA-5j5w-g665-5m35):** Ambiguous OCI manifest/index parsing (the same digest could be deserialized differently depending on the `Content-Type` header) allowed inconsistent image interpretation. This is a **Low**-severity integrity/confusion issue, fixed by rejecting ambiguous documents; it did **not** enable arbitrary file write or code execution.

---

#### **Threat ID: STORE-004**
*   **Threat:** An attacker intercepts or eavesdrops on registry communication during image pull operations due to disabled TLS or MITM attacks.
*   **STRIDE Category:** Spoofing, Information Disclosure
*   **Risk Assessment:**
    *   **Severity:** High - Exposure of private image layers (containing application code or configurations) or execution of tampered layers.
    *   **Likelihood:** Low-Medium - Requires network positioning or DNS/BGP hijacking combined with insecure configuration.
*   **Mitigation:** Enforce TLS verification for all registry endpoints. See [OPERATOR_GUIDELINES.md (registry TLS)](./OPERATOR_GUIDELINES.md#14-registry-tls).

---

#### **Threat ID: STORE-005**
*   **Threat:** An attacker with local write access tampers with content-store blobs (layers, configs) on disk after they have been pulled.
*   **STRIDE Category:** Tampering
*   **Risk Assessment:**
    *   **Non-Vulnerability:** Modifying blobs under `/var/lib/containerd` requires local write access to a root-restricted directory and is classified as a Security Exclusion (Section 2.4). It is documented here because containerd offers a defense-in-depth control against it.
*   **Mitigation:** The default content store verifies a blob's digest **at write/commit time** (and consumers such as image pull re-verify fetched content against its expected digest), but it does **not** re-hash blobs on every read — a plain content-store read serves the on-disk bytes without re-verification, so content-addressing alone is not a read-time tamper check. The available defense-in-depth control is:
    *   **`fs-verity` (opportunistic):** On supporting kernels/filesystems, the default content store enables `fs-verity` on committed blobs. The kernel then makes each blob immutable (in-place writes fail with `EPERM`) and blocks reads of a blob whose bytes were altered in place (`EIO`) — the only mechanism providing read-time at-rest integrity for stored blobs. It does **not** stop a root attacker who deletes and replaces a blob with a fresh `fs-verity`-enabled file (containerd does not enforce `fs-verity` signatures), and it does **not** apply to STORE-001 (image-unpacking) threats. See [fsverity.md](../fsverity.md).

---

#### **Threat ID: STORE-006**
*   **Threat:** A maliciously crafted image supplies a native EROFS layer, which the EROFS differ writes to the snapshotter without unpacking; the host kernel's EROFS driver then parses the attacker-controlled bytes when the snapshot is mounted.
*   **STRIDE Category:** Denial of Service (DoS), Elevation of Privilege (EoP)
*   **Risk Assessment:**
    *   **Severity:** High - The flaw exploited would be in the in-kernel EROFS driver rather than in containerd, with containerd acting as the vector that delivers the crafted bytes to the kernel. Impact is bounded by what a kernel filesystem defect allows, up to host compromise.
    *   **Likelihood:** Low - Requires the EROFS differ to be configured and the image to advertise an EROFS layer media type; the default pull path unpacks tar layers in userspace instead.
*   **Mitigation:** Keep the host kernel updated — containerd does not validate the internal structure of a native EROFS blob before mounting it, so the kernel's own hardening is the control. Restrict which images can reach this path by pinning to digests and verifying image signatures. See [OPERATOR_GUIDELINES.md (image integrity)](./OPERATOR_GUIDELINES.md#tier-2--recommended--advanced-hardening).

---

### 4.4 CRI Plugin

---

#### **Threat ID: CRI-001**
*   **Threat:** A vulnerability in the CRI plugin's request handling leads to resource exhaustion (memory, CPU) or daemon crash.
*   **STRIDE Category:** Denial of Service (DoS)
*   **Risk Assessment:**
    *   **Severity:** High - Knocks out containerd, causing orchestrator status sync loss and scheduling failures.
    *   **Likelihood:** Medium - Unprivileged users can transitively trigger CRI API endpoints (e.g., creating high volume exec resizes or logs).
*   **Mitigation:** Strict input validation, rate limiting, and resource controls inside the CRI plugin.
*   **Real-world Examples:**
    *   **[GHSA-2qjp-425j-52j9](https://github.com/containerd/containerd/security/advisories/GHSA-2qjp-425j-52j9) ([CVE-2022-23471](https://github.com/containerd/containerd/security/advisories/GHSA-2qjp-425j-52j9)):** Terminal resize goroutine leak leading to host memory exhaustion.
    *   **[GHSA-5ffw-gxpp-mxpf](https://github.com/containerd/containerd/security/advisories/GHSA-5ffw-gxpp-mxpf) ([CVE-2022-31030](https://github.com/containerd/containerd/security/advisories/GHSA-5ffw-gxpp-mxpf)):** Memory exhaustion via `ExecSync` calls.
    *   **[GHSA-m6hq-p25p-ffr2](https://github.com/containerd/containerd/security/advisories/GHSA-m6hq-p25p-ffr2) ([CVE-2025-64329](https://github.com/containerd/containerd/security/advisories/GHSA-m6hq-p25p-ffr2)):** Goroutine leaks in `Attach` functionality leading to memory exhaustion.

---

#### **Threat ID: CRI-002**
*   **Threat:** The CRI plugin incorrectly handles volume mounts or security contexts, allowing a container to access host filesystem nodes or execute setuid binaries to escalate privileges.
*   **STRIDE Category:** Elevation of Privilege (EoP), Information Disclosure
*   **Risk Assessment:**
    *   **Severity:** High - Bypasses security profiles (like SELinux labels), enabling host namespace traversal or local privilege escalation.
    *   **Likelihood:** Medium - Unprivileged users can submit Pod Specs requesting volume bindings.
*   **Mitigation:** Enforce `nosuid` on local volume mounts. Set `allowPrivilegeEscalation: false` on pods. Use admission controllers to block insecure mounts. See [OPERATOR_GUIDELINES.md (admission controllers)](./OPERATOR_GUIDELINES.md#tier-2--recommended--advanced-hardening).
*   **Real-world Examples:**
    *   **[GHSA-mvff-h3cj-wj9c](https://github.com/containerd/containerd/security/advisories/GHSA-mvff-h3cj-wj9c) ([CVE-2021-43816](https://github.com/containerd/containerd/security/advisories/GHSA-mvff-h3cj-wj9c)):** Unprivileged pods using `hostPath` could sidestep SELinux labels.
    *   **[GHSA-crp2-qrr5-8pq7](https://github.com/containerd/containerd/security/advisories/GHSA-crp2-qrr5-8pq7) ([CVE-2022-23648](https://github.com/containerd/containerd/security/advisories/GHSA-crp2-qrr5-8pq7)):** Insecure handling of image volumes allowed arbitrary file reading.

---

#### **Threat ID: CRI-003**
*   **Threat:** A flaw in containerd's own handling of the workload identity (user/UID/GID) causes a container to run with a more privileged identity than the spec requested, bypassing an orchestrator-enforced policy such as `runAsNonRoot`.
*   **STRIDE Category:** Elevation of Privilege (EoP)
*   **Risk Assessment:**
    *   **Severity:** Moderate - The container gains an unintended identity (e.g., root *inside the container*). This does not by itself cross the container/host boundary, but it defeats a security control the orchestrator relied on.
    *   **Likelihood:** Medium - Triggerable by an unprivileged tenant supplying a crafted user field in their own Pod/container spec.
*   **Mitigation:** Keep containerd updated. Enforce identity policies with admission controllers as defense-in-depth rather than relying solely on the runtime.
*   **Real-world Examples:**
    *   **[GHSA-265r-hfxg-fhmg](https://github.com/containerd/containerd/security/advisories/GHSA-265r-hfxg-fhmg) ([CVE-2024-40635](https://github.com/containerd/containerd/security/advisories/GHSA-265r-hfxg-fhmg)):** An integer overflow in containerd's User ID handling caused containers configured with a UID above the 32-bit signed maximum to wrap around to `0`, running as root inside the container and bypassing `runAsNonRoot`. This is a containerd bug (not a `runc` flaw) and is an in-container identity bypass, not a host escape.

---

#### **Threat ID: CRI-004**
*   **Threat:** Much of what an image config carries (such as the `entrypoint`, environment variables, working directory, and `USER` directive) configures the workload itself. By design, containerd applies these settings to the workload. A trust boundary is crossed only if containerd or one of its plugins interprets an image-config value as runtime control input or uses it to make a host-side decision without validating it as untrusted Class 2 content (Section 2.2). One example is an image label copied into a reserved namespace (`containerd.io/*` or `io.cri-containerd*`): because containerd and its plugins read those labels as their own control metadata (for example, a restart-monitor log URI that the shim executes), an image that sets one is steering the runtime rather than labeling its workload. Another example is an image-derived path or mount source used for a host-side file operation without validation.
*   **STRIDE Category:** Elevation of Privilege (EoP), Information Disclosure
*   **Risk Assessment:**
    *   **Severity:** Critical - Depending on the sink, an interpreted image-config value can reach arbitrary host command execution or host file disclosure.
    *   **Likelihood:** Medium-High - The attacker only needs containerd to pull and run their image (see the malicious-image note in Section 4.0; High in multi-tenant clusters).
*   **Mitigation:** containerd should not let image-config values be interpreted as containerd's own control input: reject or strip image-config labels in reserved namespaces rather than copying them onto the container, and validate image-derived paths and mount sources before any host-side use. Keep containerd updated and verify image signatures to reject untrusted images before they run. See [OPERATOR_GUIDELINES.md (image integrity)](./OPERATOR_GUIDELINES.md#tier-2--recommended--advanced-hardening).
*   **Real-world Examples:**
    *   **[GHSA-xhf5-7wjv-pqxp](https://github.com/containerd/containerd/security/advisories/GHSA-xhf5-7wjv-pqxp) ([CVE-2026-53488](https://github.com/containerd/containerd/security/advisories/GHSA-xhf5-7wjv-pqxp)):** Image-config `LABEL` values were copied unvalidated into container labels; a reserved restart-monitor label (`containerd.io/restart.loguri`) was interpreted as a `binary://` log URI and executed as host root. containerd now drops reserved-namespace labels when copying from an image config.
    *   **[GHSA-crp2-qrr5-8pq7](https://github.com/containerd/containerd/security/advisories/GHSA-crp2-qrr5-8pq7) ([CVE-2022-23648](https://github.com/containerd/containerd/security/advisories/GHSA-crp2-qrr5-8pq7)):** Insecure handling of image-declared volume paths allowed arbitrary host file reads (also referenced under CRI-002 as a volume/mount issue).

---

#### **Threat ID: CRI-005**
*   **Threat:** CRIU-based container checkpoint/restore introduces a distinct untrusted input: the checkpoint archive and its embedded metadata. A tenant supplying a crafted checkpoint image controls the archive's file contents, embedded annotations, and recorded image references, all of which represent untrusted Class 2 content (Section 2.2). The threat occurs if containerd derives runtime configuration from this attacker-authored metadata during import or restore instead of using the live Pod request, allowing host-side effects to land before `runc` or CRIU starts. Examples include annotations carried in the archive applied to the restored container's OCI spec (devices, mounts, hooks), image references recorded in the checkpoint config written into the node-local image store, and archive file paths (such as `container.log`) resolved against the host filesystem.
*   **STRIDE Category:** Elevation of Privilege (EoP), Spoofing, Tampering, Information Disclosure
*   **Risk Assessment:**
    *   **Severity:** Critical - The impact depends on the sink: injection of devices or hooks into the restored OCI spec, disclosure of a host file, or a poisoned node-local image tag that affects other workloads.
    *   **Likelihood:** Medium - Requires checkpoint/restore to be in use and the attacker to supply a crafted checkpoint image.
*   **Mitigation:** containerd should treat the checkpoint archive as untrusted content: re-derive security-relevant OCI configuration from the live Pod request rather than the archive, validate or strip embedded annotations, do not create node-local image references from the archive, and resolve restored file paths without following symlinks. Users should restrict checkpoint import to trusted sources and keep containerd updated.
*   **Real-world Examples:**
    *   **[GHSA-33vj-92qq-66hc](https://github.com/containerd/containerd/security/advisories/GHSA-33vj-92qq-66hc) ([CVE-2026-53492](https://github.com/containerd/containerd/security/advisories/GHSA-33vj-92qq-66hc)):** CDI annotations carried in checkpoint metadata were re-applied to the restored OCI spec, which injected attacker-chosen devices, mounts, or hooks (OCI-spec sink).
    *   **[GHSA-cvxm-645q-p574](https://github.com/containerd/containerd/security/advisories/GHSA-cvxm-645q-p574) ([CVE-2026-50195](https://github.com/containerd/containerd/security/advisories/GHSA-cvxm-645q-p574)):** Checkpoint import created a node-local image tag from the archive's image reference, which poisoned the node's image cache (image-store sink).
    *   **[GHSA-rgh6-rfwx-v388](https://github.com/containerd/containerd/security/advisories/GHSA-rgh6-rfwx-v388) ([CVE-2026-53489](https://github.com/containerd/containerd/security/advisories/GHSA-rgh6-rfwx-v388)):** `container.log` was restored by following a symlink in the archive, which exposed an arbitrary host file via `kubectl logs` (host-filesystem sink).

---

### 4.5 Trusted Computing Base (TCB)

---

#### **Threat ID: TCB-001**
*   **Threat:** A vulnerability in an external TCB component (host kernel, system shims, or external plugins like CNI/NRI) allows host compromise.
*   **STRIDE Category:** Elevation of Privilege (EoP), Tampering, Information Disclosure, Denial of Service (DoS)
*   **Risk Assessment:**
    *   **Severity:** Critical - Vulnerability in root-equivalent layers (like CNI or NRI plugins) can lead to full host compromise.
    *   **Likelihood:** Low - Highly variable; while runc escapes (like CVE-2024-21626) present high exploitability once found, TCB compromise generally requires admin setup of custom plugins or zero-day system flaws.
*   **Mitigation:** Ensure CNI/NRI extensions are vetted, securely configured, and updated. See [OPERATOR_GUIDELINES.md (trusted plugins)](./OPERATOR_GUIDELINES.md#15-trusted-plugins).

---

## 5. Threat Summary Matrix

| Threat ID | Subsystem | Category | Threat Description | Severity | Likelihood |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **CORE-001** | Core Daemon | EoP / Tampering | Unauthorized socket access | Non-Vuln | - |
| **CORE-002** | Core Daemon | DoS | Request handling crash / resource exhaustion | High | Medium |
| **CORE-003** | Core Daemon | DoS / Tampering | BoltDB metadata tampering | Non-Vuln | - |
| **CORE-004** | Core Daemon | DoS | Events queue DoS / amplification | Non-Vuln | - |
| **CORE-005** | Core Daemon | Tampering | `config.toml` tampering | Non-Vuln | - |
| **CORE-006** | Core Daemon | Info / EoP | World-traversable directory traversal | High | Medium |
| **RUN-001** | Runtime/Shim | EoP | runc container escape (TCB dependency) | Critical | Medium |
| **RUN-002** | Runtime/Shim | EoP / Tampering | runc binary replacement | Non-Vuln | - |
| **RUN-003** | Runtime/Shim | EoP | Custom runtime wrapper escape | Critical | Low-Medium |
| **RUN-004** | Runtime/Shim | DoS | Host resource exhaustion (cgroups) | High | High |
| **RUN-005** | Runtime/Shim | EoP | Shim control-socket cross-namespace escape | Moderate | Low-Medium |
| **RUN-006** | Runtime/Shim | Repudiation | Unaudited container process execs | Moderate | High |
| **RUN-007** | Runtime/Shim | Info | Secrets leakage via environment variables | High | High |
| **STORE-001** | Storage | Tampering / EoP | Image-unpack symlink / path traversal → host write | Critical | Medium-High |
| **STORE-002** | Storage | DoS | Disk space storage exhaustion | Moderate | Medium |
| **STORE-003** | Storage | DoS / EoP | Malicious image parser exploit | High | Medium-High |
| **STORE-004** | Storage | Spoofing / Info | Registry MITM / eavesdropping | High | Low-Medium |
| **STORE-005** | Storage | Tampering | At-rest content-store blob tampering | Non-Vuln | - |
| **STORE-006** | Storage | DoS / EoP | Malicious native EROFS layer parsed by kernel driver | High | Low |
| **CRI-001** | CRI Plugin | DoS | CRI request memory/goroutine DoS | High | Medium |
| **CRI-002** | CRI Plugin | EoP / Info | Volume mount / setuid context bypass | High | Medium |
| **CRI-003** | CRI Plugin | EoP | Identity/UID bypass (`runAsNonRoot`) | Moderate | Medium |
| **CRI-004** | CRI Plugin | EoP / Info | Image-config value interpreted as runtime control input | Critical | Medium-High |
| **CRI-005** | CRI Plugin | EoP / Spoof / Tamp / Info | Attacker-authored checkpoint metadata re-trusted as runtime policy | Critical | Medium |
| **TCB-001** | TCB Plugins | EoP / Tamp | External CNI/NRI plugin vulnerability | Critical | Low |

---

## 6. References

*   **containerd Security Hub:** https://containerd.io/security
*   **[CVE-2024-25621](https://nvd.nist.gov/vuln/detail/CVE-2024-25621)** — Local privilege escalation via world-traversable directory permissions.
*   **[CVE-2025-64329](https://nvd.nist.gov/vuln/detail/CVE-2025-64329)** — Goroutine leak in CRI plugin attach interface leading to memory DoS.
*   **[CVE-2025-47290](https://nvd.nist.gov/vuln/detail/CVE-2025-47290)** — Symlink TOCTOU path traversal during image unpacking (containerd 2.1.0 regression, fixed in 2.1.1).
*   **[CVE-2024-40635](https://github.com/containerd/containerd/security/advisories/GHSA-265r-hfxg-fhmg)** — Integer overflow in containerd UID handling; large UIDs wrap to root inside the container, bypassing `runAsNonRoot` (containerd, not runc).
*   **[GHSA-36xw-fx78-c5r4](https://github.com/containerd/containerd/security/advisories/GHSA-36xw-fx78-c5r4) ([CVE-2020-15257](https://nvd.nist.gov/vuln/detail/CVE-2020-15257))** — Privilege escalation via shim abstract Unix socket exposure from `hostNetwork` workloads (rated Moderate by containerd).
*   **runc escape CVEs:** [CVE-2024-21626](https://nvd.nist.gov/vuln/detail/CVE-2024-21626) / [CVE-2025-31133](https://nvd.nist.gov/vuln/detail/CVE-2025-31133) / [CVE-2025-52565](https://nvd.nist.gov/vuln/detail/CVE-2025-52565) / [CVE-2025-52881](https://nvd.nist.gov/vuln/detail/CVE-2025-52881) (runc escapes via fd leak / mount operations).
*   **[CVE-2026-53488](https://github.com/containerd/containerd/security/advisories/GHSA-xhf5-7wjv-pqxp):** Image-config `LABEL` propagated to a reserved restart-monitor label, yielding host-root command execution via a `binary://` log URI (CRI-004).
*   **[CVE-2026-53492](https://github.com/containerd/containerd/security/advisories/GHSA-33vj-92qq-66hc):** CDI annotation smuggling during CRI checkpoint restore (CRI-005, OCI-spec sink).
*   **[CVE-2026-50195](https://github.com/containerd/containerd/security/advisories/GHSA-cvxm-645q-p574):** Local image-tag poisoning via CRI checkpoint import (CRI-005, image-store sink).
*   **[CVE-2026-53489](https://github.com/containerd/containerd/security/advisories/GHSA-rgh6-rfwx-v388):** Arbitrary host file read via symlink following in CRI checkpoint restore (CRI-005, host-filesystem sink).
