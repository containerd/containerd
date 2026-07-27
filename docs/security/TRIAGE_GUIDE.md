# containerd Security Triage Guide

This guide outlines how containerd maintainers and security advisors triage incoming security reports.

> [!NOTE]
> To **report** a security vulnerability, use the [containerd Security Advisories portal](https://github.com/containerd/containerd/security). This guide describes how maintainers triage and evaluate reports *after* submission.

## 1. Security Boundaries and Threat Model

The primary security boundary for containerd is the **container isolation boundary**. A vulnerability is typically defined as a flaw that allows an attacker to bypass this boundary (e.g., container escape).

### 1.1. Root-Equivalence

containerd considers access to its primary APIs to be **equivalent to root access on the host**. This includes:

*   **containerd gRPC Socket:** Typically located at `/run/containerd/containerd.sock`. By default, the CRI plugin is served over this same socket.

**Triage Impact:** Reports that require access to these sockets to exploit are often classified as **bugs or hardening items** rather than vulnerabilities, unless they lead to an unexpected elevation of privilege beyond what an authorized client should be able to achieve. For example, if an authorized CRI client can write arbitrary files to the host filesystem, this is considered "by design" because a CRI client can already create privileged containers with full host access.

### 1.2. Trusted vs. Untrusted Input

containerd distinguishes between input from trusted sources (e.g., authorized API clients, trusted registries) and untrusted sources (e.g., malicious container images, untrusted users in a container).

*   **Image Unpacking:** Vulnerabilities in image unpacking (e.g., path traversal) are treated as high-priority vulnerabilities because containerd is expected to safely handle potentially malicious images from remote registries.
*   **Metadata:** Oversized metadata fields (labels, extensions) are treated as Denial of Service (DoS) vulnerabilities if they can lead to resource exhaustion of the daemon.

### 1.3. Triage Criteria for Architectural Boundaries
When assessing incoming security reports, containerd maintainers follow the architectural boundaries defined in [THREAT_MODEL.md, Section 2.3 (Security Scope and Expectations)](./THREAT_MODEL.md#23-security-scope-and-expectations):
*   **Socket Access Reports:** Reports requiring direct, local access to `containerd.sock` are closed as **Non-Vulnerabilities** (unless they bypass the Kubelet CRI translation layer).
*   **Stack Escape Reports:** Escapes originating from flaws in `runc` or the Linux kernel are referred upstream and closed as **TCB dependencies**.
*   **Third-Party Plugin Reports:** Flaws in CNI or NRI plugins are referred to their respective plugin maintainers, as all plugins run inside the TCB.
*   **Error Leakage Reports:** Registry token or credential leaks inside error messages are triaged as **Hardening opportunities**, not vulnerabilities.

## 2. CVSS Scoring Stance

containerd follows a specific philosophy when calculating CVSS scores to ensure consistency and accuracy.

### 2.1. Attack Vector (AV) and Privileges Required (PR)

containerd CVSS scoring distinguishes between direct socket access and transitive orchestrator access:
*   **Direct Socket Access:** If an exploit needs direct, local access to the containerd socket (restricted to root or admin GIDs), the Base AV is **Local (L)** and Privileges Required is **High (H)**.
*   **Transitive Orchestrator Access:** If the exploit is triggered transitively by an unprivileged tenant through standard orchestrator APIs (e.g., a Kubernetes Pod Spec) or registry actions (e.g., image pulls), the Base AV is **Network (N)** and Privileges Required are typically **Low (L)** (reflecting the tenant's authorized orchestrator privileges).

### 2.2. Scope (S)

We carefully consider whether a vulnerability results in a **Scope Change (C)**.
*   A container escape that allows access to the host typically constitutes a Scope Change.
*   Violating orchestrator-enforced security constraints (e.g., bypassing Kubernetes `runAsNonRoot` or `readOnlyRootFilesystem` policies) or causing persistent orchestrator management failures (e.g., preventing Pod termination) constitutes a Scope Change.
*   Host-level Denial of Service or resource exhaustion does **not** change scope (S:U); this impact is captured strictly under **Availability: High (A:H)**.

### 2.3. User Interaction (UI)

*   **None (UI:N):** If the attacker possesses standard orchestrator credentials to trigger the vulnerable action directly (e.g., deploying their own Pod).
*   **Required (UI:R):** If the attack relies on inducing an administrator or other user to pull a compromised image, run a payload, or configure optional features.

## 3. Triage Process

1.  **Reproduce:** Empirically reproduce the reported issue.
2.  **Assess Boundary:** Determine if a documented security boundary was crossed.
3.  **Check TCB:** Identify if the flaw is in containerd itself or a TCB component (e.g., runc, Linux kernel).
4.  **Assign Severity:** Use the component-first CVSS stance to determine the Base Score.
5.  **Coordinate:** If a vulnerability is confirmed, follow the security policy for coordinated disclosure, including opening a GHSA and notifying relevant stakeholders (e.g., Kubernetes SRC).

## 4. Common Non-Vulnerabilities

*   **Attacks requiring root/socket access:** As noted in 1.1, these are generally not considered vulnerabilities.
*   **Issues in `--net=host` mode:** Sharing host namespaces is inherently insecure. While we implement hardening (e.g., moving to path-based Unix sockets for shims), we do not guarantee isolation when namespaces are shared.
*   **Vulnerable Dependencies:** Vulnerabilities in dependencies are only considered containerd vulnerabilities if they are **exploitable** through containerd's usage of that dependency.
