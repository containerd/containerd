# containerd Security Triage Guide

This document outlines the principles and process for triaging security reports in the containerd project. It is intended for use by containerd maintainers and security advisors.

## 1. Security Boundaries and Threat Model

The primary security boundary for containerd is the **container isolation boundary**. A vulnerability is typically defined as a flaw that allows an attacker to bypass this boundary (e.g., container escape).

### 1.1. Root-Equivalence

containerd considers access to its primary control plane APIs to be **equivalent to root access on the host**. This includes:

*   **containerd gRPC Socket:** Typically located at `/run/containerd/containerd.sock`.
*   **CRI gRPC Socket:** Typically located at `/run/containerd/containerd.sock` (or similar, depending on configuration).

**Triage Impact:** Reports that require access to these sockets to exploit are often classified as **bugs or hardening items** rather than vulnerabilities, unless they lead to an unexpected elevation of privilege beyond what an authorized client should be able to achieve. For example, if an authorized CRI client can write arbitrary files to the host filesystem, this is considered "by design" because a CRI client can already create privileged containers with full host access.

### 1.2. Trusted vs. Untrusted Input

containerd distinguishes between input from trusted sources (e.g., authorized API clients, trusted registries) and untrusted sources (e.g., malicious container images, untrusted users in a container).

*   **Image Unpacking:** Vulnerabilities in image unpacking (e.g., path traversal) are treated as high-priority vulnerabilities because containerd is expected to safely handle potentially malicious images from remote registries.
*   **Metadata:** Oversized metadata fields (labels, extensions) are treated as Denial of Service (DoS) vulnerabilities if they can lead to resource exhaustion of the daemon.

## 2. CVSS Scoring Stance

containerd follows a specific philosophy when calculating CVSS scores to ensure consistency and accuracy.

### 2.1. Component-First Scoring

containerd is scored as an **individual component**.
*   **Attack Vector (AV):** By default, containerd's APIs are bound to local Unix domain sockets. Therefore, the Base AV is typically **Local (L)**.
*   **Privileges Required (PR):** Since access to the sockets requires root privileges (or equivalent), PR is often **High (H)**.

### 2.2. Environmental Metrics for Orchestrators

While the Base Score reflects containerd in isolation, we recognize that most users interact with containerd through an orchestrator like Kubernetes.
*   **Modified Attack Vector (MAV):** When triaging for a Kubernetes context, the MAV may be elevated to **Network (N)** if the vulnerability can be triggered via the Kubernetes API.
*   **Environmental Score:** We encourage users and distributors (like cloud providers) to use environmental metrics to reflect their specific deployment context.

### 2.3. Scope (S)

We carefully consider whether a vulnerability results in a **Scope Change (C)**.
*   A container escape that allows access to the host typically constitutes a Scope Change.
*   Denial of Service of the daemon itself (Unchanged Scope) is distinguished from DoS that impacts the entire host (potentially Changed Scope).

### 2.4. User Interaction (UI)

In containerd triage, the requirement to run a container from a malicious image or with a specific configuration is generally **not** considered "User Interaction" (UI:N).
*   While an operator or system must initiate the container, this is considered a prerequisite or part of the attack chain's execution rather than a separate user being tricked into an action (which is the intent of the UI metric in CVSS).
*   However, if an attack requires an authorized user to perform an out-of-band action (e.g., clicking a link that triggers a request to a local-only API), then **UI:R** may be appropriate.

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
