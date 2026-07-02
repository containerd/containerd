# containerd Operator Security Guidelines

This document outlines containerd's security baseline requirements and prioritized hardening suggestions for operators.

---

## 1. Baseline Security Requirements

These represent the absolute baseline requirements that containerd assumes are met. Any exploit utilizing a breach of these requirements does not violate containerd's security boundaries and is classified as a Non-Vulnerability (Security Exclusion):

### 1.1. Host & Daemon Patching
*   **Kernel Patching:** Keep the host Linux kernel updated with security patches. The host kernel is the primary container boundary; escapes often rely on unpatched kernel vulnerabilities.
*   **containerd Version Alignment:** Keep `containerd` updated to the latest stable patch releases. Refer to the official [releases support matrix](https://containerd.io/releases/#current-state-of-containerd-releases) to ensure your version is actively supported.

### 1.2. Dependency Patching (runc)
*   **runc & Shim Patching:** Keep the OCI runtime (`runc`) and runtime shims (`containerd-shim-runc-v2`) updated. Subscribe to security announcements for these components.

### 1.3. Permissions Constraints
*   **Socket Permissions:** containerd sets socket permissions to `0660` on startup. Restrict access to `containerd.sock` and `nri.sock` (usually at `/var/run/nri/nri.sock`) by assigning GID ownership to a system GID (like `kubelet` or a custom admin group) that has no unprivileged users. Allowing groups with unprivileged users to write to the socket is a major risk; socket access is equivalent to root and bypasses `sudo` auditing. Never mount these sockets inside unprivileged containers.
*   **Directory Permissions:** Enforce strict GID/UID boundaries. The daemon root directory (`/var/lib/containerd`) must use `0700` permissions, while the runtime state root (`/run/containerd`) uses `0711` to allow necessary traversal for user-namespaced workloads. Per-plugin state directories that hold sensitive data — notably the CRI directory (`io.containerd.grpc.v1.cri`), which can contain pod volume contents — should be `0700`. Do **not** blanket-tighten every subdirectory to `0700`: some runtime subdirectories are intentionally created `0711` so that UID/GID-remapped (user-namespaced) workloads can traverse them, and forcing those to `0700` can break such workloads.
*   **File Permissions:** Host binaries (`containerd`, `runc`, shims), systemd service files (like `containerd.service`), daemon environment files (like `/etc/default/containerd`), and `config.toml` must be owned by `root`. They should not be writable by unprivileged users (use `0640` or `0755` as appropriate) and must not have setuid or setgid flags.

### 1.4. Registry TLS
*   **HTTPS Enforcement:** Only connect to registries over HTTPS with verified certificates. Disable insecure HTTP registries in production.

### 1.5. Trusted Plugins
*   **Plugin Security Controls:** Vet NRI, CNI, and snapshotter plugins before using them. Make sure only `root` can write to plugin binaries (e.g., `/opt/cni/bin/`) and configurations (e.g., `/etc/cni/net.d/`, `/etc/nri/`). Plugins run inside containerd's Trusted Computing Base (TCB), so there is no security boundary between containerd and its plugins.

---

## 2. Prioritized Hardening Suggestions

These represent optional, defense-in-depth suggestions that operators can adopt to minimize the attack surface.

### Tier 1 — Highly Recommended / Basic Hardening
*   **Digest-based Image Pulls:** Pin container images, base images, and the orchestrator sandbox image (like the Kubernetes `pause` image configured in `config.toml` under `sandbox_image`) to digests (`sha256:...`) instead of tags. This stops attackers or registries from replacing the image under a tag.
*   **Minimize Namespace Sharing:** Avoid sharing host namespaces (`--net=host`, `--pid=host`, `--ipc=host`) unless absolutely required. Workloads running in host namespaces bypass container isolation by design.
*   **Isolation Profiles:** Enforce default Seccomp, AppArmor, or SELinux profiles for unprivileged workloads where supported by the host kernel and orchestrator configuration.
*   **Workload Least Privilege:** Execute container processes with the lowest possible privileges (e.g., running as non-root UIDs/GIDs, dropping unnecessary Linux capabilities). Avoid passing sensitive credentials or API keys directly inside container environment variables.

### Tier 2 — Recommended / Advanced Hardening
*   **Admission Controllers:** Deploy Kubernetes admission controllers (e.g., Kyverno, OPA Gatekeeper) to block unprivileged containers requesting host volume mounts or raw capabilities.
*   **Log & Event Monitoring:** Monitor containerd daemon logs for deprecation warnings, container OOM events, or gRPC connection anomalies.
*   **User Namespaces:** Configure UID/GID mapping for container root users to mitigate host-level breakout risks where supported by the orchestrator and storage engine.
*   **Image Integrity Verification:** Configure signature verification (cosign/Notary) and SBOM/provenance verification (SLSA) using the [containerd image verifier plugin](../image-verification.md).
*   **Secrets Protection:** Migrate secrets from environment variables to volume-mounted, memory-only tmpfs folders (e.g., Kubernetes Secrets).
*   **Disk Space Quotas:** Apply filesystem-level storage quotas on the containerd root (`/var/lib/containerd`), which holds the content store and snapshot data where layer blobs and unpacked rootfs accumulate, to prevent disk space exhaustion Denial of Service (DoS).

### Tier 3 — Defense in Depth
*   **Host Access:** Keep SSH access to host nodes to a minimum and make sure all administrative sessions are logged and audited.
*   **Sandboxed Runtimes:** Execute untrusted workloads via microkernel or VM-based sandboxed runtimes (e.g., Kata Containers, gVisor) at the OCI runtime class layer.
*   **File Integrity Monitoring (FIM):** Deploy FIM tools (AIDE, OSSEC) on host system folders to track modifications to binaries, libraries, and configuration files.
