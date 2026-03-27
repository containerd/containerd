# Threat Model: containerd

**Date:** Feb 2, 2026
---

## 1. System Overview and Scope

### 1.1 Architecture Components

containerd operates as a daemon process exposing a gRPC API, with the following major subsystems:

| Component | Description |
|---|---|
| **containerd daemon** | Central process managing container lifecycle, image storage, and runtime coordination |
| **gRPC API / Socket** | Unix socket (`/run/containerd/containerd.sock`) for client communication |
| **CRI Plugin** | Kubernetes Container Runtime Interface implementation |
| **Content Store** | Content-addressable storage for image layers and manifests |
| **Metadata Store (BoltDB)** | Stores container, image, and namespace metadata |
| **Snapshotter** | Manages filesystem snapshots for container rootfs (overlayfs, btrfs, etc.) |
| **Runtime Shims** | Per-container processes bridging containerd and low-level runtimes |
| **runc (OCI runtime)** | Low-level runtime executing containers via Linux namespaces and cgroups |
| **Image Pull/Push** | Interacts with remote OCI registries over HTTPS |
| **Namespace System** | Logical isolation of resources within containerd |

### 1.2 Trust Boundaries

The following trust boundaries are critical to the threat model:

1. **Host OS ↔ Container**: kernel-enforced isolation via namespaces, cgroups, seccomp, AppArmor/SELinux
2. **Client ↔ containerd API**: access to the Unix socket grants full control over containers
3. **containerd ↔ OCI Registry**: network boundary for image pull/push operations
4. **containerd ↔ runc/shim**: delegation of container creation to low-level runtime
5. **Container ↔ Container**: lateral isolation between co-located workloads
6. **Orchestrator (Kubernetes) ↔ containerd**: CRI interface trust boundary

### 1.3 Key Assets

- Host filesystem and kernel
- Container images (intellectual property, application code)
- Container runtime state and secrets (environment variables, mounted volumes)
- Cryptographic keys and TLS certificates
- Kubernetes secrets and ConfigMaps exposed as volumes
- The containerd socket itself

---

## 2. STRIDE Analysis

### 2.1 Spoofing

Spoofing threats involve an attacker impersonating a trusted identity to gain unauthorized access.

#### S-1: Registry Impersonation (Man-in-the-Middle on Image Pull)

- **Target:** Image pull subsystem, network boundary to OCI registries
- **Description:** An attacker intercepts or redirects image pull requests to a malicious registry, serving trojanized images that appear legitimate. DNS hijacking, BGP hijacking, or compromised CA certificates can facilitate this.
- **Impact:** Execution of arbitrary malicious code inside containers believed to be trusted.
- **Likelihood:** Medium
- **Mitigations:** Enforce TLS certificate verification for all registry connections. Use image signing and verification (e.g., cosign, Notary/TUF). Pin images by digest (`sha256:...`) rather than mutable tags. Deploy admission controllers (e.g., Kyverno, OPA Gatekeeper) to enforce signature policies.

#### S-2: Client Spoofing on the containerd Socket

- **Target:** gRPC API / Unix socket
- **Description:** Any process with filesystem access to the containerd socket can issue API calls as if it were a legitimate orchestrator. There is no built-in authentication layer on the socket beyond Unix file permissions.
- **Impact:** Full control over all containers, images, and runtime state on the host.
- **Likelihood:** Medium–High (depends on host hardening)
- **Mitigations:** Restrict socket file permissions to root or a dedicated group. Use SELinux/AppArmor to confine which processes may access the socket. In Kubernetes, never mount the containerd socket into application pods.

#### S-3: Container Image Tag Spoofing

- **Target:** Content store, image pull
- **Description:** Mutable tags (e.g., `latest`, `v1.0`) can be overwritten in a registry. An attacker who compromises a registry account or exploits a registry vulnerability can replace a trusted tag with a malicious image.
- **Impact:** Deployment of compromised workloads across the fleet on next pull.
- **Likelihood:** Medium
- **Mitigations:** Reference images by immutable digest. Implement registry access controls with least privilege. Use image signing verification at deployment time.

---

### 2.2 Tampering

Tampering threats involve unauthorized modification of data, code, or configuration.

#### T-1: Malicious Image Layer Injection (Supply Chain Attack)

- **Target:** Content store, image pull pipeline
- **Description:** An attacker injects a malicious layer into a base image upstream, compromising all downstream builds. This can occur via compromised CI/CD pipelines, dependency confusion, or registry compromise.
- **Impact:** Widespread execution of attacker-controlled code across all containers built from the compromised image.
- **Likelihood:** Medium–High
- **Mitigations:** Scan images with vulnerability scanners (Trivy, Grype) at build and deploy time. Use Software Bill of Materials (SBOM) generation and verification. Pin base images by digest. Implement image provenance attestations (SLSA framework).

#### T-2: Container Filesystem Escape via Symlink/TOCTOU Attacks

- **Target:** Snapshotter, image unpacking, runc mount operations
- **Description:** A maliciously crafted container image manipulates symlinks or exploits race conditions during image unpacking to write outside the container's filesystem boundary onto the host. This is a real and recurring vulnerability class — CVE-2025-47290 demonstrated exactly this pattern in containerd 2.1.0, and CVE-2025-31133/52565/52881 in runc exploited mount operations for container escape.
- **Impact:** Arbitrary file write on the host filesystem, leading to full host compromise.
- **Likelihood:** Medium (requires crafted image)
- **Mitigations:** Keep containerd and runc updated to latest patched versions. Use read-only rootfs where possible. Enable user namespaces to limit the impact of filesystem escapes. Deploy runtime security tools (Falco, Sysdig) to detect symlink manipulation.

#### T-3: Tampering with containerd Configuration

- **Target:** containerd daemon configuration (`/etc/containerd/config.toml`)
- **Description:** An attacker with host access modifies the containerd configuration to disable security features, redirect image pulls to malicious registries, or load malicious plugins.
- **Impact:** Undermining all container isolation and security controls.
- **Likelihood:** Low–Medium (requires host access)
- **Mitigations:** Protect configuration files with strict permissions (0600, root-owned). Use file integrity monitoring (AIDE, OSSEC). Implement immutable infrastructure patterns where config is baked into the host image.

#### T-4: Metadata Store (BoltDB) Manipulation

- **Target:** `/var/lib/containerd/` metadata
- **Description:** An attacker with local access tampers with BoltDB metadata to alter container configurations, re-map image references, or inject malicious runtime parameters.
- **Impact:** Containers may start with altered security contexts, or trusted image references may point to malicious content.
- **Likelihood:** Low (requires local privileged access)
- **Mitigations:** Restrict `/var/lib/containerd/` permissions to 0700 (addressed by CVE-2024-25621 fix). Use filesystem encryption. Monitor for unexpected writes to containerd data directories.

---

### 2.3 Repudiation

Repudiation threats involve a user denying an action without the system being able to prove otherwise.

#### R-1: Unattributed Container Operations

- **Target:** gRPC API, containerd namespaces
- **Description:** containerd's native API does not enforce per-user authentication or produce detailed audit logs of who performed container operations (create, exec, delete). In multi-tenant or shared-host scenarios, actions cannot be reliably attributed to a specific user or service.
- **Impact:** Inability to investigate security incidents, attribute malicious container deployments, or satisfy compliance audit requirements.
- **Likelihood:** High (by default)
- **Mitigations:** Enable Kubernetes audit logging at the CRI layer. Deploy system-level audit frameworks (auditd, Falco) to log containerd socket access and container lifecycle events. Implement centralized log aggregation with tamper-evident storage. Use containerd namespace separation to isolate tenant operations.

#### R-2: Container Exec Without Audit Trail

- **Target:** Runtime shims, container exec interface
- **Description:** An operator or attacker runs `exec` into a running container to perform actions (data exfiltration, backdoor installation) without these commands being logged by containerd itself.
- **Impact:** Post-incident forensics cannot determine what commands were run inside containers.
- **Likelihood:** High
- **Mitigations:** Deploy runtime observability tools (Falco, Tetragon, Tracee) that capture process execution inside containers via eBPF. Log all `exec` API calls through Kubernetes audit logs. Enforce policies that restrict exec access to authorized roles.

---

### 2.4 Information Disclosure

Information disclosure threats involve exposure of sensitive data to unauthorized parties.

#### I-1: Secrets Leakage via Environment Variables

- **Target:** Container runtime configuration, CRI plugin
- **Description:** Secrets passed as environment variables to containers are visible in the container's `/proc/[pid]/environ` on the host, in containerd's metadata, and potentially in logs and diagnostic dumps.
- **Impact:** Exposure of API keys, database credentials, and other secrets.
- **Likelihood:** High
- **Mitigations:** Use dedicated secret management solutions (Vault, Kubernetes Secrets with encryption at rest, sealed-secrets). Avoid passing secrets as environment variables; prefer volume-mounted secret files. Restrict access to `/proc` on the host.

#### I-2: Overly Permissive Directory Permissions

- **Target:** `/var/lib/containerd/`, `/run/containerd/` directory trees
- **Description:** containerd historically created runtime directories with overly broad permissions (0711, 0755 instead of 0700), as documented in CVE-2024-25621. This allowed local users to traverse directories and potentially access container metadata, content store data, and Kubernetes local volume contents.
- **Impact:** Local information disclosure; exposure of setuid binaries from Kubernetes volumes could enable further privilege escalation.
- **Likelihood:** Medium
- **Mitigations:** Update to containerd versions that fix CVE-2024-25621. Verify directory permissions after upgrades. Use security benchmarks (CIS Benchmarks) to audit host configuration.

#### I-3: Image Layer Content Exposure

- **Target:** Content store
- **Description:** Container image layers in the content store are accessible to any process that can read `/var/lib/containerd/`. These layers may contain embedded secrets, proprietary code, or sensitive configuration.
- **Impact:** Intellectual property theft, credential exposure.
- **Likelihood:** Medium
- **Mitigations:** Restrict content store directory permissions. Avoid embedding secrets in container images. Use multi-stage builds to minimize sensitive data in final images. Scan images for accidental secret inclusion.

#### I-4: Network Eavesdropping on Registry Traffic

- **Target:** Image pull/push, registry communication
- **Description:** If TLS is misconfigured or disabled for registry connections, image pull traffic (including potentially private images) can be intercepted.
- **Impact:** Exposure of proprietary application code and configurations embedded in images.
- **Likelihood:** Low (TLS is default, but misconfigurations happen)
- **Mitigations:** Enforce TLS for all registry connections. Never configure `--insecure-registry` in production. Use mutual TLS (mTLS) where possible. Monitor for plaintext registry traffic.

---

### 2.5 Denial of Service (DoS)

DoS threats prevent legitimate users from accessing system resources.

#### D-1: Resource Exhaustion via Container Workloads

- **Target:** Host resources (CPU, memory, disk I/O, PIDs)
- **Description:** A malicious or misconfigured container consumes all available host resources, starving other containers and host processes including containerd itself.
- **Impact:** Host instability, container orchestration failures, potential cascading outages.
- **Likelihood:** High
- **Mitigations:** Enforce cgroup resource limits (CPU, memory, PIDs) on all containers. Configure Kubernetes LimitRanges and ResourceQuotas. Set disk quotas on snapshotter storage. Monitor resource consumption with alerting.

#### D-2: Goroutine / Memory Leak in containerd Daemon

- **Target:** containerd daemon process
- **Description:** Certain API call patterns or malformed inputs can trigger goroutine leaks leading to memory exhaustion of the containerd process. CVE-2025-64329 documented exactly this vulnerability, where goroutine leaks could cause memory exhaustion on the host.
- **Impact:** containerd daemon becomes unresponsive, all container management operations fail.
- **Likelihood:** Medium
- **Mitigations:** Keep containerd updated with security patches. Configure daemon resource limits via systemd (e.g., `MemoryMax`). Deploy health checks and automatic restart policies. Monitor containerd process metrics (goroutine count, memory usage).

#### D-3: Image Pull Bomb / Storage Exhaustion

- **Target:** Content store, snapshotter
- **Description:** An attacker triggers pulls of extremely large or deeply layered images to exhaust disk space on the host, or creates an excessive number of snapshots.
- **Impact:** Host disk full, preventing new container creation and potentially crashing running containers.
- **Likelihood:** Medium
- **Mitigations:** Set disk space limits and quotas for containerd storage. Implement image pull rate limiting. Configure garbage collection policies for unused images and snapshots. Use separate partitions for containerd storage.

#### D-4: API Flooding on containerd Socket

- **Target:** gRPC API
- **Description:** A compromised process with socket access floods the containerd API with requests, overwhelming the daemon and blocking legitimate orchestrator operations.
- **Impact:** Container management paralysis.
- **Likelihood:** Low–Medium
- **Mitigations:** Restrict socket access to authorized processes only. Monitor API request rates. Implement connection limits at the gRPC layer.

---

### 2.6 Elevation of Privilege

Elevation of privilege threats involve an attacker gaining higher-level access than authorized.

#### E-1: Container Breakout to Host

- **Target:** Container isolation boundary (namespaces, cgroups, seccomp, LSMs)
- **Description:** An attacker exploits a kernel vulnerability, a misconfigured container (e.g., `--privileged`, excessive capabilities), or a runtime vulnerability (runc CVE-2025-31133/52565/52881) to escape the container and gain root access on the host. This is the most critical threat class for any container runtime.
- **Impact:** Full host compromise; lateral movement to all containers on the host; potential cluster-wide compromise.
- **Likelihood:** Medium (recurring vulnerability class)
- **Mitigations:** Never run containers as root or with `--privileged` unless absolutely necessary. Drop all unnecessary Linux capabilities. Enable seccomp profiles (default Docker profile at minimum). Enable AppArmor/SELinux for mandatory access control. Use user namespaces to map container root to unprivileged host user. Consider sandboxed runtimes (gVisor, Kata Containers) for untrusted workloads. Keep kernel, runc, and containerd fully patched.

#### E-2: Privilege Escalation via containerd Socket Access

- **Target:** gRPC API / Unix socket
- **Description:** A user or process that gains access to the containerd socket (e.g., through a container with the socket mounted, or via group membership) can create privileged containers, mount the host filesystem, and effectively gain root on the host.
- **Impact:** Full host compromise from any container or process with socket access.
- **Likelihood:** Medium–High
- **Mitigations:** Never mount the containerd socket into containers. Treat socket access as equivalent to root access. Audit group membership for the containerd socket group. Use Pod Security Standards/Admission to block socket mounts in Kubernetes.

#### E-3: Exploiting Setuid Binaries in Volume Mounts

- **Target:** Kubernetes local volumes, containerd volume handling
- **Description:** Linked to CVE-2024-25621's overly permissive directory creation — if Kubernetes volumes contain setuid binaries and the enclosing directories are world-traversable, a local attacker can execute those binaries to escalate privileges.
- **Impact:** Local privilege escalation on the host.
- **Likelihood:** Low–Medium
- **Mitigations:** Apply `nosuid` mount option on volume mounts. Fix directory permissions per CVE-2024-25621 patches. Minimize use of hostPath volumes. Scan volumes for setuid/setgid binaries.

#### E-4: Malicious or Vulnerable Plugins

- **Target:** containerd plugin system (snapshotters, runtime plugins, CRI extensions)
- **Description:** containerd's modular plugin architecture allows loading external plugins via gRPC. A malicious or vulnerable plugin runs with the same privileges as the containerd daemon (typically root), enabling arbitrary host access.
- **Impact:** Full host compromise via compromised plugin.
- **Likelihood:** Low (requires administrative action to install)
- **Mitigations:** Audit all third-party plugins before deployment. Use only plugins from trusted sources. Monitor plugin loading via audit logs. Minimize the number of external plugins.

---

## 3. Threat Summary Matrix

| ID | Category | Threat | Severity | Likelihood |
|---|---|---|---|---|
| S-1 | Spoofing | Registry impersonation / MITM | High | Medium |
| S-2 | Spoofing | Client spoofing on containerd socket | Critical | Medium–High |
| S-3 | Spoofing | Image tag overwrite | High | Medium |
| T-1 | Tampering | Supply chain image injection | Critical | Medium–High |
| T-2 | Tampering | Filesystem escape via symlink/TOCTOU | Critical | Medium |
| T-3 | Tampering | Configuration tampering | High | Low–Medium |
| T-4 | Tampering | Metadata store manipulation | Medium | Low |
| R-1 | Repudiation | Unattributed container operations | Medium | High |
| R-2 | Repudiation | Exec without audit trail | Medium | High |
| I-1 | Information Disclosure | Secrets via environment variables | High | High |
| I-2 | Information Disclosure | Permissive directory permissions | High | Medium |
| I-3 | Information Disclosure | Image layer content exposure | Medium | Medium |
| I-4 | Information Disclosure | Registry traffic eavesdropping | Medium | Low |
| D-1 | Denial of Service | Resource exhaustion via containers | High | High |
| D-2 | Denial of Service | Goroutine/memory leak in daemon | High | Medium |
| D-3 | Denial of Service | Storage exhaustion via image pulls | Medium | Medium |
| D-4 | Denial of Service | API flooding | Medium | Low–Medium |
| E-1 | Elevation of Privilege | Container breakout to host | Critical | Medium |
| E-2 | Elevation of Privilege | Privilege escalation via socket | Critical | Medium–High |
| E-3 | Elevation of Privilege | Setuid exploitation in volumes | High | Low–Medium |
| E-4 | Elevation of Privilege | Malicious plugin loading | Critical | Low |

---

## 4. Priority Mitigations

Based on the analysis above, the following mitigations address the highest-risk threats and should be prioritized:

### Tier 1 — Immediate / Critical

1. **Patch management**: Keep containerd, runc, and the host kernel current. Subscribe to containerd security advisories.
2. **Socket access control**: Restrict the containerd socket to root only. Never mount it into containers. Treat socket access as root-equivalent.
3. **Image integrity**: Sign and verify all container images. Pin by digest. Deploy admission controllers to enforce image policies.
4. **Least privilege containers**: Drop all unnecessary capabilities. Enforce non-root execution. Enable seccomp and AppArmor/SELinux profiles.
5. **Resource limits**: Enforce cgroup limits (CPU, memory, PIDs) on every container.

### Tier 2 — High Priority

6. **Audit and observability**: Deploy Falco or Tetragon for runtime syscall monitoring. Enable Kubernetes audit logging. Aggregate logs to tamper-evident storage.
7. **Secret management**: Migrate from environment variables to volume-mounted secrets with encryption at rest.
8. **Directory permissions**: Verify that `/var/lib/containerd/` and `/run/containerd/` trees have 0700 permissions (CVE-2024-25621 fix applied).
9. **Network security**: Enforce TLS for all registry connections. Consider network policies to restrict container egress.

### Tier 3 — Defense in Depth

10. **Sandboxed runtimes**: Use gVisor or Kata Containers for untrusted or multi-tenant workloads.
11. **User namespaces**: Enable user namespace remapping to limit container-breakout impact.
12. **Immutable infrastructure**: Use read-only host filesystems and immutable container images.
13. **Plugin auditing**: Inventory and review all loaded containerd plugins periodically.

---

## 5. References

- https://containerd.io/security: containerd security policy and audits
- [CVE-2024-25621](https://nvd.nist.gov/vuln/detail/CVE-2024-25621) — Local privilege escalation via directory permissions
- [CVE-2025-64329](https://nvd.nist.gov/vuln/detail/CVE-2025-64329) — Goroutine leak leading to memory exhaustion
- [CVE-2025-47290](https://nvd.nist.gov/vuln/detail/CVE-2025-47290) — TOCTOU vulnerability in image unpacking (containerd 2.1.0)
- [CVE-2025-31133](https://nvd.nist.gov/vuln/detail/CVE-2025-31133) / [CVE-2025-52565](https://nvd.nist.gov/vuln/detail/CVE-2025-52565) / [CVE-2025-52881](https://nvd.nist.gov/vuln/detail/CVE-2025-52881) — runc container escape via mount operations

---

*This threat model should be reviewed and updated whenever containerd is upgraded, architecture changes occur, or new CVEs are disclosed.*
