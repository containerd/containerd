# containerd Threat Model

## 1. Introduction

This document provides a threat model for the containerd container runtime. The goal of this threat model is to identify potential security risks, categorize them using the STRIDE methodology, and propose mitigations. This document also includes a set of security guidelines for operators of containerd.

This threat model considers the default configuration of containerd, as well as popular extensions and configurations.

## 2. Architecture Overview

containerd is a high-level container runtime that manages the complete container lifecycle of its host system, from image transfer and storage to container execution and supervision to low-level storage to network attachments and beyond.

The architecture of containerd is based on a "smart client" model, where the containerd daemon provides a minimal core set of services, and clients are responsible for more complex operations like creating container specifications and interacting with image registries.

The key components of the containerd architecture are:

*   **containerd Daemon:** The central process that manages all other components. It exposes a gRPC API over a UNIX socket.
*   **gRPC API:** The primary control plane for containerd. Access to this API is equivalent to root access on the host. The default location is `/run/containerd/containerd.sock`.
*   **Clients:** Tools like `ctr`, `nerdctl`, and CRI clients (e.g., kubelet) that interact with the containerd daemon via the gRPC API.
*   **Shims:** Long-running processes that are responsible for the lifecycle of a container. containerd uses a "shim v2" API, which allows for different runtime implementations. The default shim is `containerd-shim-runc-v2`.
*   **Runtimes:** Low-level container runtimes that are responsible for creating and running container processes. The default runtime is `runc`, which implements the OCI Runtime Specification.
*   **Snapshotters:** Plugins that manage the lifecycle of container filesystems (layers). The default snapshotter is `overlayfs`. Snapshotters can be built-in or external proxy plugins.
*   **Content Store:** A component that stores immutable content, such as container image layers.
*   **Metadata Store:** A boltDB database that stores metadata about containers, images, namespaces, and other resources.

The architecture is highly modular, with many components implemented as plugins. This allows for flexibility and extensibility, but also introduces a larger attack surface.

### 2.1. Trusted Computing Base (TCB)

The Trusted Computing Base (TCB) includes all components that are critical to the security of the system. A component is considered part of the TCB if a vulnerability or misconfiguration within it could lead to a compromise of the host system. This includes not only components that run with high privileges (e.g., as `root`), but also those that can **influence or control privileged operations**, such as by modifying OCI runtime specifications. For containerd, the TCB includes, but is not limited to:

*   **The `containerd` daemon:** The central privileged process.
*   **Core Plugins:** All plugins compiled into `containerd`, including default snapshotters (`overlayfs`, `native`), content store, metadata store, and CRI plugin.
*   **Runtimes & Shims:**
    *   **Low-level runtime:** Such as `runc`.
    *   **Shim:** Such as `containerd-shim-runc-v2`.
*   **External Plugins:** Any configured third-party plugins. These plugins interact with `containerd` through different mechanisms:
    *   **Proxy Plugins:** External gRPC services that replace a built-in service (e.g., a proxy snapshotter).
    *   **gRPC Service Plugins:** External services that integrate via a dedicated gRPC API, such as **NRI (Node Resource Interface) plugins**.
    *   **Executable Plugins:** External binaries executed by `containerd` for specific tasks (e.g., CNI plugins, Image Verifier plugins, and some custom logging solutions).

Operators should assume that any component in this list must be trusted.

## 3. Security Guidelines for containerd Operators

Based on the threat model, here is a set of security guidelines for operators of containerd:

### 3.1. Host System Security

*   **Keep the Host Kernel Patched:** The kernel is the ultimate security boundary. Keep it up-to-date to protect against known container escape vulnerabilities.
*   **Use a Hardened Kernel:** Consider using a kernel with security-hardening features, such as `grsecurity` or `PAX`.
*   **Secure Critical Binaries:** Ensure that critical binaries like `containerd`, `containerd-shim-runc-v2`, and `runc` have strict file permissions and are owned by root. Use file integrity monitoring (FIM) to detect unauthorized changes.
*   **Apply the Principle of Least Privilege:** Do not run unnecessary services on the host. Limit user access to the host system.

### 3.2. containerd Configuration

*   **Secure the gRPC Socket:** The containerd gRPC socket should only be accessible by the `root` user (or owner of the daemon's user namespace) and trusted processes (like `kubelet`). Do not expose the socket over the network.
*   **Use Resource Limits:** Configure `cgroup` limits for containers to prevent resource exhaustion attacks (DoS).
*   **Enable User Namespaces:** For workloads that support it, use user namespaces to map the container's root user to a non-privileged user on the host. This significantly reduces the impact of a container escape.
*   **Use Security Profiles:** Use Seccomp, AppArmor, or SELinux to restrict the capabilities of containers. The default Seccomp profile provided by containerd is a good starting point, but it may need to be tailored for specific workloads.
*   **Manage Storage:** Configure storage quotas to prevent DoS attacks. Regularly prune unused images, containers, and snapshots.
*   **Minimize Host Namespace Sharing:** Avoid using host networking (`--net=host` or `hostNetwork: true`), host PID, or host IPC namespaces unless absolutely necessary. Sharing these namespaces significantly reduces container isolation and increases the risk of host compromise.

### 3.3. CRI and Kubernetes Security

*   **Secure the CRI Socket:** Access to the CRI gRPC socket is equivalent to root access on the node. Ensure it has strict filesystem permissions (typically `0660` owned by `root:root` or a dedicated group) and is only accessible by the `kubelet`.
*   **Use Admission Controllers:** Use Kubernetes admission controllers (like Pod Security Admissions) to enforce security policies and prevent the creation of overly privileged pods.
*   **Monitor for Warnings:** Monitor containerd logs for deprecation warnings related to oversized metadata fields or other security-related events.

### 3.4. Image Security

*   **Use Trusted Images:** Only use container images from trusted sources.
*   **Verify Image Integrity:** Use image signing and verification tools like Notary or cosign. Configure the containerd image verifier plugin to enforce policies that require all images to be signed.
*   **Scan Images for Vulnerabilities:** Integrate a vulnerability scanner into your CI/CD pipeline to scan images for known vulnerabilities.
*   **Don't Leak Secrets:** Do not embed secrets, private keys, or other sensitive data in container images. Use a dedicated secret management solution (like HashiCorp Vault or Kubernetes Secrets). When building images, use multi-stage builds to avoid including build-time secrets and tools in the final image. Use tools like `.dockerignore` to prevent sensitive files from being accidentally included in the build context. Scan images for secrets before pushing them to a registry.

### 3.5. Plugin and Extension Security

*   **Vet All Plugins:** All plugins (snapshotters, runtimes, CNI, NRI, etc.) should be treated as part of the trusted computing base. Carefully vet any third-party plugins before using them in production. NRI (Node Resource Interface) plugins, in particular, have significant control over container specifications and should be rigorously audited.
*   **Apply Least Privilege to Plugins:** If possible, run plugins with the minimum required privileges. For example, a proxy snapshotter might not need to run as root.

## 4. Threat Model

This section breaks down the threats to containerd using the STRIDE and DREAD models.

### 4.1. Core Daemon & gRPC API

---

### **Threat ID: CORE-001**

**Threat:** An unauthorized user or process gains access to the containerd gRPC socket.

**STRIDE Category:**
*   Elevation of Privilege (EoP)
*   Tampering
*   Information Disclosure

**DREAD Risk:**
*   **Damage:** High (3) - Full control over all containers and images.
*   **Reproducibility:** High (3) - Trivial if file permissions are incorrect.
*   **Exploitability:** Medium (2) - Requires local access to the host.
*   **Affected Users:** High (3) - All users of the system.
*   **Discoverability:** High (3) - The socket location is well-known.

**Mitigation:**
By default, the containerd socket is only accessible by the `root` user. Administrators should follow the principle of least privilege and not grant unnecessary access to this socket. While a granular authorization plugin has been discussed in the community, it is not a built-in feature. Therefore, securing the socket's file permissions is the primary mitigation.

---

### **Threat ID: CORE-002**

**Threat:** A vulnerability in the containerd daemon's gRPC request handling logic leads to a crash or resource exhaustion.

**STRIDE Category:**
*   Denial of Service (DoS)

**DREAD Risk:**
*   **Damage:** Medium (2) - Crashes the daemon, but can be restarted.
*   **Reproducibility:** Medium (2) - Depends on the specific vulnerability.
*   **Exploitability:** Low (1) - Requires access to the gRPC socket.
*   **Affected Users:** High (3) - All users of the system.
*   **Discoverability:** Low (1) - Requires source code analysis or fuzzing.

**Mitigation:**
The containerd daemon should have robust input validation and resource limits on all gRPC endpoints. The daemon should be run under a process supervisor (like systemd) that can restart it if it crashes.

**Real-world Examples:**
*   **[GHSA-259w-8hf6-59c2](https://github.com/containerd/containerd/security/advisories/GHSA-259w-8hf6-59c2) ([CVE-2023-25153](https://github.com/containerd/containerd/security/advisories/GHSA-259w-8hf6-59c2)):** Lack of memory limits during OCI image import could lead to memory exhaustion (DoS).

---

### **Threat ID: CORE-003**


**Threat:** An attacker with host filesystem access or control over the containerd daemon tampers with the containerd metadata store (boltDB), leading to a denial of service or inconsistent state.

**STRIDE Category:**
*   Denial of Service (DoS)
*   Tampering

**DREAD Risk:**
*   **Damage:** Medium (2) - Loss of metadata, but can be rebuilt.
*   **Reproducibility:** Low (1) - Requires a specific sequence of events or a vulnerability.
*   **Exploitability:** Low (1) - Requires access to the host filesystem or compromise of containerd.
*   **Affected Users:** High (3) - All users of the system.
*   **Discoverability:** Low (1) - Hard to discover without triggering an error.

**Mitigation:**
The metadata store should have strict filesystem permissions, preventing unauthorized direct access. Any process with write access to this file is highly privileged already.

---

### **Threat ID: CORE-004**

**Threat:** An attacker, with access to the containerd gRPC socket, can rapidly perform API calls that generate a high volume of events. This sustained event generation can overwhelm the events-handling subsystem, causing excessive serialization and deserialization, continuous memory allocation, and significant queueing and dispatching overhead within the containerd daemon. The resulting high CPU and memory usage can lead to reduced daemon responsiveness, event loss, or delayed delivery, ultimately causing a denial of service for components and orchestrators that rely on timely event processing.

**STRIDE Category:**
*   Denial of Service (DoS)

**DREAD Risk:**
*   **Damage:** Low (1) - Temporary DoS of the events service.
*   **Reproducibility:** High (3) - Easy to do with access to the gRPC socket.
*   **Exploitability:** Low (1) - Requires gaining local access to the host and then the gRPC socket.
*   **Affected Users:** Medium (2) - Users relying on the events service.
*   **Discoverability:** Medium (2) - The general attack vector is known, but specific effective scenarios might be harder to discover.

**Mitigation:**
The events service should have rate limiting and resource controls in place.

---

### 4.2. Runtime and Shims

The container runtime and shim are the components most directly responsible for enforcing the container boundary. A vulnerability in this layer is the most likely path to a container escape (Elevation of Privilege).

---

### **Threat ID: RUN-001**

**Threat:** A vulnerability in the low-level runtime (`runc`) allows a malicious process in a container to escape to the host.

**STRIDE Category:**
*   Elevation of Privilege (EoP)

**DREAD Risk:**
*   **Damage:** High (3) - Full host compromise.
*   **Reproducibility:** Medium (2) - Depends on the vulnerability, but many escape exploits are reliable.
*   **Exploitability:** High (3) - Can be exploited from within a container.
*   **Affected Users:** High (3) - The user of the compromised container, and potentially all users of the host.
*   **Discoverability:** Low (1) - Discovering new, exploitable kernel and runtime vulnerabilities is a specialized and difficult task.

**Mitigation:**
Keep `runc` and the host kernel patched and up-to-date. Use security profiles like Seccomp, AppArmor, or SELinux to restrict the syscalls available to the container. Use hardened kernels and other host security features. Utilize user namespaces to map the container's root user to a non-privileged user on the host.

**Real-world Examples:**
*   **[GHSA-265r-hfxg-fhmg](https://github.com/containerd/containerd/security/advisories/GHSA-265r-hfxg-fhmg) ([CVE-2024-40635](https://github.com/containerd/containerd/security/advisories/GHSA-265r-hfxg-fhmg)):** An integer overflow in User ID handling allowed containers with large UIDs to overflow and run as UID 0 (root).

---

### **Threat ID: RUN-002**

**Threat:** An attacker replaces the `runc` binary on the host with a malicious version.

**STRIDE Category:**
*   Elevation of Privilege (EoP)
*   Tampering

**DREAD Risk:**
*   **Damage:** High (3) - Full host compromise.
*   **Reproducibility:** Low (1) - Requires gaining root access to the host.
*   **Exploitability:** Low (1) - Requires gaining root access to the host.
*   **Affected Users:** High (3) - All users of the system.
*   **Discoverability:** Medium (2) - Can be discovered with file integrity monitoring.

**Mitigation:**
The `runc` binary should have strict file permissions. Use file integrity monitoring (FIM) tools to detect unauthorized changes to critical system binaries.

---

### **Threat ID: RUN-003**

**Threat:** A misconfigured custom runtime allows for a container escape.

**STRIDE Category:**
*   Elevation of Privilege (EoP)

**DREAD Risk:**
*   **Damage:** High (3) - Full host compromise.
*   **Reproducibility:** Low (1) - Requires an attacker to first discover a specific misconfiguration in a custom runtime and then reliably reproduce the conditions for a container escape, which would typically involve significant reverse-engineering or analysis effort.
*   **Exploitability:** Low (1) - Exploiting a custom runtime misconfiguration demands deep understanding of the flaw and how to leverage it for privilege escalation, a non-trivial task without prior detailed knowledge.
*   **Affected Users:** High (3) - All users of the custom runtime.
*   **Discoverability:** Medium (2) - Can be discovered by reviewing the runtime's configuration and code.

**Mitigation:**
Custom runtimes should be carefully vetted and tested. They should be developed with a security-first mindset and follow best practices for secure coding.

---

### **Threat ID: RUN-004**

**Threat:** A container process is able to exhaust host resources (CPU, memory, PIDs) because of weak or missing cgroup limits.

**STRIDE Category:**
*   Denial of Service (DoS)

**DREAD Risk:**
*   **Damage:** Medium (2) - Can make the host unusable.
*   **Reproducibility:** High (3) - Trivial to do from within a container.
*   **Exploitability:** High (3) - No special privileges needed.
*   **Affected Users:** High (3) - All users of the host.
*   **Discoverability:** High (3) - Easy to discover by running a resource-intensive process.

**Mitigation:**
Always apply appropriate resource limits to containers, reviewing and adjusting them based on the specific workload.

---

### **Threat ID: RUN-005**

**Threat:** A vulnerability in the shim's I/O handling (e.g., fifo/named pipe processing) leads to a crash or information disclosure.

**STRIDE Category:**
*   Denial of Service (DoS)
*   Information Disclosure

**DREAD Risk:**
*   **Damage:** Medium (2) - Can crash the shim or leak data.
*   **Reproducibility:** Low (1) - Reproducibility is low unless a specific vulnerability in the I/O handling logic is already known.
*   **Exploitability:** Low (1) - Likely requires a malicious container image or other vector.
*   **Affected Users:** Medium (2) - Users of the compromised container.
*   **Discoverability:** Low (1) - Requires source code analysis or fuzzing.

**Mitigation:**
The I/O handling code should be robust and handle edge cases gracefully. Additionally, containerd shims now use path-based Unix sockets instead of abstract sockets to allow for filesystem-based ACLs, preventing unauthorized access from processes in the same network namespace.

**Real-world Examples:**
*   **[GHSA-36xw-fx78-c5r4](https://github.com/containerd/containerd/security/advisories/GHSA-36xw-fx78-c5r4) ([CVE-2020-15257](https://github.com/containerd/containerd/security/advisories/GHSA-36xw-fx78-c5r4)):** The containerd-shim API was exposed via abstract Unix sockets, allowing processes with UID 0 in the same network namespace (e.g., in a `hostNetwork` container) to connect and execute new processes with escalated privileges.

---

### 4.3. Storage (Snapshotters and Content Store)

Storage components are responsible for managing container images and filesystems. Vulnerabilities in this area can lead to data tampering, information disclosure, or denial of service.

---

### **Threat ID: STORE-001**

**Threat:** An attacker tampers with image data in the content store on disk.

**STRIDE Category:**
*   Tampering
*   Elevation of Privilege (EoP)

**DREAD Risk:**
*   **Damage:** High (3) - Can lead to running malicious code.
*   **Reproducibility:** High (3) - Trivial if the attacker has access to the filesystem.
*   **Exploitability:** Low (1) - Requires gaining access to the host filesystem.
*   **Affected Users:** High (3) - All users of the compromised image.
*   **Discoverability:** Medium (2) - The lack of continuous on-disk verification is a subtle but important detail.

**Mitigation:**
`containerd` does not continuously verify the integrity of image layers on disk after they have been pulled. Therefore, the primary mitigations against on-disk tampering are:
1.  **Strict filesystem permissions:** The content store directory should have strict permissions to prevent unauthorized modification.
2.  **File Integrity Monitoring (FIM):** FIM tools should be used to detect and alert on any unauthorized changes to the content store.
3.  **fs-verity:** On systems that support it, `containerd` will automatically use `fs-verity` to provide transparent integrity protection for content. See [the fs-verity documentation](./docs/fsverity.md) for configuration details.

**Real-world Examples:**
*   **[GHSA-cm76-qm8v-3j95](https://github.com/containerd/containerd/security/advisories/GHSA-cm76-qm8v-3j95) ([CVE-2025-47290](https://github.com/containerd/containerd/security/advisories/GHSA-cm76-qm8v-3j95)):** A path traversal vulnerability during image unpacking allowed writing files to arbitrary locations on the host filesystem.
*   **[GHSA-c72p-9xmj-rx3w](https://github.com/containerd/containerd/security/advisories/GHSA-c72p-9xmj-rx3w) ([CVE-2021-32760](https://github.com/containerd/containerd/security/advisories/GHSA-c72p-9xmj-rx3w)):** The archive package allowed `chmod` of files outside of the unpack target directory.

---

### **Threat ID: STORE-002**

**Threat:** An attacker fills the disk by pulling large images, causing a denial of service.

**STRIDE Category:**
*   Denial of Service (DoS)

**DREAD Risk:**
*   **Damage:** Medium (2) - Can make the host unusable.
*   **Reproducibility:** High (3) - Trivial to do with access to the gRPC socket.
*   **Exploitability:** Low (1) - This attack requires privileged access to the gRPC socket, which is a high barrier to entry.
*   **Affected Users:** High (3) - All users of the host.
*   **Discoverability:** High (3) - Easy to discover.

**Mitigation:**
Storage quotas can be configured at the filesystem level or through snapshotter-specific options. For example:
1.  **Filesystem Quotas:** Use operating system tools (like `xfs_quota` or `quota`) to enforce size limits on the filesystem that backs `/var/lib/containerd`.
2.  **Snapshotter Configuration:** Some snapshotters, like `devmapper`, have size-related options (e.g., `base_image_size`) that can help manage storage.
3.  **Regular Pruning:** Independently of quotas, regularly prune unused images and other resources to manage disk space.

---

### **Threat ID: STORE-003**

**Threat:** An attacker crafts a malicious image that exploits a vulnerability in the content store's parser.

**STRIDE Category:**
*   Denial of Service (DoS)
*   Elevation of Privilege (EoP)

**DREAD Risk:**
*   **Damage:** High (3) - Could lead to remote code execution in the containerd daemon.
*   **Reproducibility:** Low (1) - Depends on a specific, unknown vulnerability in the parser.
*   **Exploitability:** Medium (2) - Requires the ability to push a malicious image to a registry.
*   **Affected Users:** High (3) - All users of the host, as it could lead to host compromise.
*   **Discoverability:** Low (1) - Requires fuzzing the image parser.

**Mitigation:**
The content store's image parsing logic should be robust and handle malformed data. The parser should be regularly fuzzed.

**Real-world Examples:**
*   **[GHSA-5j5w-g665-5m35](https://github.com/containerd/containerd/security/advisories/GHSA-5j5w-g665-5m35):** Ambiguous OCI manifest parsing could lead to inconsistent state or potential security bypasses. (Note: This advisory does not have an assigned CVE.)

---

### 4.4. CRI Plugin

The CRI (Container Runtime Interface) plugin is responsible for implementing the Kubernetes CRI API. It interacts with both the containerd daemon and the shims/runtimes.

---

### **Threat ID: CRI-001**

**Threat:** A vulnerability in the CRI plugin's request handling leads to resource exhaustion or a crash of the containerd daemon.

**STRIDE Category:**
*   Denial of Service (DoS)

**DREAD Risk:**
*   **Damage:** Medium (2) - Crashes the daemon or impacts responsiveness.
*   **Reproducibility:** High (3) - Trivial to trigger via CRI API.
*   **Exploitability:** Medium (2) - Requires access to the CRI API (e.g., from kubelet).
*   **Affected Users:** High (3) - All users of the Kubernetes cluster.
*   **Discoverability:** Medium (2) - Attack vectors are similar to general API DoS.

**Mitigation:**
Implement robust input validation, rate limiting, and resource quotas within the CRI plugin.

**Real-world Examples:**
*   **[GHSA-2qjp-425j-52j9](https://github.com/containerd/containerd/security/advisories/GHSA-2qjp-425j-52j9) ([CVE-2022-23471](https://github.com/containerd/containerd/security/advisories/GHSA-2qjp-425j-52j9)):** Terminal resize goroutine leak leading to host memory exhaustion.
*   **[GHSA-5ffw-gxpp-mxpf](https://github.com/containerd/containerd/security/advisories/GHSA-5ffw-gxpp-mxpf) ([CVE-2022-31030](https://github.com/containerd/containerd/security/advisories/GHSA-5ffw-gxpp-mxpf)):** Memory exhaustion via `ExecSync` calls.
*   **[GHSA-m6hq-p25p-ffr2](https://github.com/containerd/containerd/security/advisories/GHSA-m6hq-p25p-ffr2) ([CVE-2025-64329](https://github.com/containerd/containerd/security/advisories/GHSA-m6hq-p25p-ffr2)):** Goroutine leaks in `Attach` functionality leading to memory exhaustion.

---

### **Threat ID: CRI-002**

**Threat:** The CRI plugin incorrectly handles volume mounts or security contexts, allowing a container to access unauthorized host resources or bypass security policies.

**STRIDE Category:**
*   Elevation of Privilege (EoP)
*   Information Disclosure

**DREAD Risk:**
*   **Damage:** High (3) - Potential for host filesystem access or security bypass.
*   **Reproducibility:** Medium (2) - Depends on specific pod configurations.
*   **Exploitability:** Medium (2) - Requires the ability to create pods with specific configurations.
*   **Affected Users:** High (3) - Users of the Kubernetes cluster.
*   **Discoverability:** Medium (2) - Requires understanding of CRI and container security mechanisms.

**Mitigation:**
Ensure strict validation of volume paths and security contexts. Use mandatory access control (MAC) like SELinux or AppArmor to provide defense-in-depth.

**Real-world Examples:**
*   **[GHSA-mvff-h3cj-wj9c](https://github.com/containerd/containerd/security/advisories/GHSA-mvff-h3cj-wj9c) ([CVE-2021-43816](https://github.com/containerd/containerd/security/advisories/GHSA-mvff-h3cj-wj9c)):** Unprivileged pods using `hostPath` could sidestep SELinux labels.
*   **[GHSA-crp2-qrr5-8pq7](https://github.com/containerd/containerd/security/advisories/GHSA-crp2-qrr5-8pq7) ([CVE-2022-23648](https://github.com/containerd/containerd/security/advisories/GHSA-crp2-qrr5-8pq7)):** Insecure handling of image volumes.

---

### 4.5. Trusted Computing Base (TCB)

---

### **Threat ID: TCB-001**

**Threat:** A vulnerability in an external Trusted Computing Base (TCB) component that `containerd` depends on allows for privilege escalation, data tampering, information disclosure, or denial of service.

**STRIDE Category:**
*   Elevation of Privilege (EoP)
*   Tampering
*   Information Disclosure
*   Denial of Service (DoS)

**DREAD Risk:**
*   **Damage:** High (3) - A vulnerability in any TCB component can lead to full host compromise.
*   **Reproducibility:** Low (1) - Depends on a specific, unknown vulnerability in an external component.
*   **Exploitability:** Low (1) - Exploiting such a vulnerability is highly dependent on the specific component and flaw.
*   **Affected Users:** High (3) - All users of the host.
*   **Discoverability:** Low (1) - Discovering new, exploitable vulnerabilities in TCB components is a specialized and difficult task.

**Mitigation:**
Ensure all external TCB components are from a trusted source, kept up-to-date with security patches, and securely configured. Refer to the TCB list in Section 2.1 for a list of components that this applies to, including the host kernel, runtimes, shims, and any external plugins (Proxy, gRPC Service, or Executable).

---
*This is a living document and will be updated as more threats are identified.*
