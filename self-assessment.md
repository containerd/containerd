# Self-assessment
The Self-assessment is the initial document for projects to begin thinking about the
security of the project, determining gaps in their security, and preparing any security
documentation for their users. This document is ideal for projects currently in the
CNCF **sandbox** as well as projects that are looking to receive a joint assessment and
currently in CNCF **incubation**.

For a detailed guide with step-by-step discussion and examples, check out the free 
Express Learning course provided by Linux Foundation Training & Certification: 
[Security Assessments for Open Source Projects](https://training.linuxfoundation.org/express-learning/security-self-assessments-for-open-source-projects-lfel1005/).

# Self-assessment outline

## Table of contents

* [Metadata](#metadata)
  * [Security links](#security-links)
* [Overview](#overview)
  * [Actors](#actors)
  * [Actions](#actions)
  * [Background](#background)
  * [Goals](#goals)
  * [Non-goals](#non-goals)
* [Self-assessment use](#self-assessment-use)
* [Security functions and features](#security-functions-and-features)
* [Project compliance](#project-compliance)
* [Secure development practices](#secure-development-practices)
* [Security issue resolution](#security-issue-resolution)
* [Appendix](#appendix)

## Metadata

A table at the top for quick reference information, later used for indexing.

|   |  |
| -- | -- |
| Software | https://github.com/containerd/containerd  |
| Security Provider | No  |
| Languages | Go, C++ |
| SBOM | [Packages](https://github.com/containerd/containerd/tree/main/pkg) [Versions](https://github.com/containerd/containerd/tree/main/version) |
| | |

### Security links

Provide the list of links to existing security documentation for the project. You may
use the table below as an example:
| Doc | url |
| -- | -- |
| Security file | https://github.com/containerd/project/blob/main/SECURITY.md |
| Default and optional configs | https://github.com/containerd/containerd/blob/main/docs/man/containerd-config.toml.5.md  https://github.com/containerd/containerd/blob/main/docs/cri/config.md  https://github.com/containerd/containerd/blob/main/docs/hosts.md |

## Overview

Containerd is a Cloud Native Computing Foundation (CNCF) Project focused on providing the core functionalities for container orchestration. Specifically architected to focus on modularity and compatibility, this provides a secure and minimal approach making it a great option for integrating into different container systems. 

![Sample Image](https://github.com/containerd/containerd/blob/main/docs/historical/design/architecture.png)

### Background

Containerd, a fundamental tool in the realm of containerization, provides a dependable and standardized approach to managing containers. It is a lightweight yet powerful container runtime, ensuring a consistent and efficient experience.

Originally developed by Docker, Inc. as an integral part of the Docker project, Containerd has evolved with the dynamic container ecosystem. Docker's decision to separate container runtime functionality led to Containerd, an independent project dedicated to container management.

#### Core Features:

**- Image and Container Management:**

Containerd oversees the entire lifecycle of containers, handling tasks such as image storage, transfer, execution, and supervision. Its capabilities also extend to other essential operations like pushing, pulling, and managing container images.

**- Pluggable Architecture:**

Containerd boasts a modular and adaptable architecture, allowing for the assembly and reassembly of independent components. This flexibility caters to the diverse requirements of container environments.

**- Security:**

With a strong emphasis on security, Containerd implements features like user namespaces and seccomp profiles. These measures enhance container isolation, ensuring a robust security posture.

**- Compatibility:**

Aligned with the Open Container Initiative (OCI) specifications, Containerd ensures compatibility with other runtimes and tools adhering to the OCI standard. This compatibility facilitates easy transitions between container runtimes supporting OCI.

**- CLI and APIs:**

Containerd provides well-defined APIs for programmatic interaction with container runtimes. Additionally, its Command-Line Interface (CLI) allows manual management of containers and images.

**- Production Ready:**

Widely adopted in multiple container orchestration platforms and cloud-native environments, Containerd has proven itself as a production-ready solution. Its reliability is evidenced by its integration into various deployments of containerized applications.

**- Community and Governance:**

As an open-source project under the Cloud Native Computing Foundation (CNCF), Containerd benefits from a diverse community of contributors. This collaborative approach ensures transparent decision-making, promoting inclusiveness and continuous improvement.

### Actors

**- Containerd Core:**

Role: Serves as the core orchestration engine, managing the execution of container-related actions.
Significance: Defines the fundamental behavior of the container runtime, providing the essential framework for container management.

**- Container Runtimes:**

Role: Executes containers based on specifications provided by containerd, interacting directly with the underlying operating system.
Significance: Key players responsible for translating container configurations into actual running instances, ensuring compatibility and adherence to standards.

**- Image Registries:**

Role: Acts as repositories for container images, collaborating with containerd in tasks such as image pulling, pushing, and managing metadata.
Significance: Critical components for image distribution, storage, and retrieval, forming a pivotal part of the containerized ecosystem.

**- System Administrators:**

Role: Configures, monitors, and maintains containerd in the broader system context, overseeing its integration into the overall infrastructure.
Responsibilities: Involves setup, continuous monitoring, optimization, and troubleshooting of containerd to ensure seamless operation.

**- Developers/Contributors:**

Role: Actively contributes to the containerd project through codebase enhancements, bug fixes, and feature development.
Responsibilities: Shapes the evolution of containerd, addressing issues, introducing improvements, and ensuring the project's ongoing robustness.

**- End Users:**
Role: Leverage containerd for deploying, managing, and orchestrating containerized applications.
Interaction: Engage with containerd through various interfaces and tools, contributing to the widespread adoption and integration of containerized solutions.

### Actions

**- Container Lifecycle Management:**

Description: Orchestrates the complete lifecycle of containers, covering creation, initialization, termination, and removal.
Significance: Acts as the backbone of container orchestration, ensuring the smooth execution of containerized applications throughout their lifecycle.

**- Image Operations:**

Description: Manages various image-related operations, including pulling images from repositories, pushing images to registries, and handling image metadata.
Significance: Central to image management within the container ecosystem, enabling efficient distribution and storage of container images.

**- Resource Isolation and Management:**

Description: Enforces robust resource isolation for individual containers, including CPU, memory, and network resources.
Significance: Optimizes resource utilization, preventing interference between containers and ensuring performance isolation.

**- Network Configuration:**

Description: Configures and manages network settings for containers, facilitating communication and maintaining network isolation.
Significance: Ensures effective container communication while safeguarding against security vulnerabilities through proper network segmentation.

**- Security Implementation:**

Description: Implements comprehensive security measures within containers, covering access controls, encrypted communication, and permission management.
Significance: Strengthens the overall security posture of containerized applications, mitigating potential vulnerabilities and ensuring secure execution.

### Goals

**- Component Independence:**

Components should not have tight dependencies on each other, allowing them to be used independently while maintaining a natural flow when used together.

**- Primitives over Abstractions:**

Containerd should expose primitives to solve problems instead of building high-level abstractions in the API. This allows flexibility for higher-level implementations.

**- Extensibility:**

Containerd should provide defined extension points for various components, allowing alternative implementations to be swapped. For example, it uses runc as the default runtime but supports other runtimes conforming to the OCI Runtime specification.

**- Defaults:**

Containerd comes with default implementations for various components, chosen by maintainers. These defaults should only change if better technology emerges.

**- Scope Clarity:**

The project scope is clearly defined, and any changes require a 100% vote from all maintainers. The whitelist approach ensures that anything not mentioned in scope is considered out of scope.

### Non-goals

**- Component Tight Coupling:**

Components should not have tight dependencies, promoting independence.

**- High-Level Abstractions in API:**

Avoid building high-level abstractions in the API, focus on exposing primitives.

**- Acceptance of Additional Implementations:**

Additional implementations for core components should not be accepted into the core repository and should be developed separately.

**- Build as a First-Class API:**

Building images is considered a higher-level feature and is out of scope.

**- Volume Management:**

Volume management for external data is out of scope. The API supports mounts, binds, etc., allowing different volume systems to be built on top.

**- Logging Persistence:**

Logging persistence is considered out of scope. Clients can handle and persist container STDIO as needed.

## Self-assessment use

This self-assessment is created by the Containerd team to perform an internal analysis of the project's security.  It is not intended to provide a security audit of Containerd, or function as an independent assessment or attestation of Containerd's security health.

This document serves to provide Containerd users with an initial understanding of Containerd's security, where to find existing security documentation, Containerd plans for security, and general overview of Containerd security practices, both for development of Containerd as well as security of Containerd.

This document provides the CNCF TAG-Security with an initial understanding of Containerd to assist in a joint-assessment, necessary for projects under incubation.  Taken together, this document and the joint-assessment serve as a cornerstone for if and when Containerd seeks graduation and is preparing for a security audit.

## Security functions and features

#### Critical

**- Namespaces:**

Namespaces creates more security and efficiency by allowing multiple consumers to use the same containerd without conflicts. It has the benefit of separation of containers and images, while sharing content. Addionally, it keeps the designs as simple as it needs to be.

**- Capabilities:**

Containerd pushes toward a least-privilege process for managing access. This limits kernel capabilities for processes. Other systems with less least-privilege could create vulnerabilities and increase their attack surfaces.

**- Isolation:**

With its capability systems and namespaces, containerd provides industry standard resource isolation, ensuring the resources remain isolated and secured. Resource isolation is crucial for namespaces to function as intended and vice versa.

**- Modularity:**

Containerd allows people to use different container systems. This gives users of containerd authority over runtimes, but if not properly handled, could lead to severe access.

#### Security Relevant

**- Plug-ins:**

Containerd allows external plugins like AppArmor and Seccomp which can decrease the attack surface of the container management. However, this also creates separate challenges not managable directly from a Containerd implementation.

**- Network Security:**

Containerd allows for network isolation, helping lockdown containers with network changes. This prevents unauthorzed communication, but needs to be monitored properly.

**- Trust:**

Containerd only stores identical content once, reducing risk of storing multiple copies of vulnerable content, thereby reducing the attack surface. If more things are uploaded, this needs to be monitored as it has a big effect on the attack surface.

## Project compliance

Containerd is not documented as meeting any major security standards except for having bypassed a test in fuzzing. The testing done by Adacompliance deemed that the fuzzing prevention was strong and with further testing was incredibly robust for industry application.

It is reasonable to suggest its minimal framework could support CIS Benchmarks on least privilege and access control policies in ISO. However, there is no public documentation with proof to having matched any of these requirements. 

## Secure development practices

**Development pipeline:**

- Containerd contributors must sign commits to ensure contributor identity and prevent unauthorized code changes.  
- Containerd images are immutable and signed. Additionally, all images are signed with a GPG key, which helps to verify the authenticity of the image.
- Continuous integration and deployment pipelines automatically test all changes in Containerd, enabling prompt issue detection. 
- The open-source code, hosted on GitHub, encourages transparency and community involvement in reviews, aiding in early issue detection.
- All pull requests to the containerd codebase must be reviewed by at least two reviewers before they can be merged.
- Compliant with industry standards, including NIST SP 800-190 and CIS Docker Benchmark, Containerd prioritizes security and reliability benchmarks. It integrates with image scanning tools (Clair, Synk, Trivy, etc.), promoting trusted image registries.
- Containerd employs privilege-dropping techniques, supports Seccomp profiles, and can operate in unprivileged user mode to minimize attack surfaces and limit security impact. 
- Resource quotas and cgroups enforce fair resource allocation, preventing resource exhaustion attacks in Containerd.
- TLS encryption safeguards data exchange, and secure networking configurations and communication protocols protect against unauthorized access. 
- The use of secure communication protocols, such as HTTPS, when communicating with external services to protect data from exposure is also promoted.
- Security audits occur regularly (CNCF fuzzing audit, community-driven audits, etc.) complemented by a responsible disclosure policy for discreetly reporting and addressing security issues before public disclosure.
- Containerd releases updates with security patches, performance enhancements, and bug fixes, while comprehensive documentation guides secure deployment (https://containerd.io/docs/).

**Communication Channels:**

- *Internal*: The Containerd team mostly communicates with each other through Slack, GitHub, or email lists internally.
- *Inbound*: Prospective and existing users can communicate with the Containerd team through GitHub issues, mailing lists, or the dedicated Slack channel.
- *Outbound*: The containerd team communicates with its users through the containerd blog, social media channels such as Twitter and GitHub, and through mailing lists.

**Ecosystem:**

Containerd plays a pivotal role in the cloud-native ecosystem due to its core functionality as a lightweight container runtime, its integration with various container orchestration platforms, and its active participation in open-source projects. This makes it an essential component for building, deploying, and managing scalable and reliable cloud-native applications.


## Security issue resolution

**- Responsible Disclosures Process**:

The responsible disclosure process for containerd is designed to manage the identification of security issues, incidents, or vulnerabilities, whether discovered internally or externally. If a security issue is found within the project team, it is reported using the same procedures as external reports. External discoveries are encouraged to follow a responsible disclosure process, which involves reporting the issue either on GitHub or via email. GitHub is the primary platform, allowing individuals to navigate to the security tab, access the Advisories tab, and use the "Report a vulnerability" option. Alternatively, an email can be sent to security@containerd.io, including details of the issue and steps to reproduce. Reporters should anticipate an acknowledgment within 24 hours and are advised to contact any committer directly if there's no response.

**- Vulnerability Response Process**:

The responsibility for responding to a reported vulnerability rests with the committers of containerd. Once a committer confirms the relevance of the reported vulnerability, a draft security advisory is created on GitHub. Reports can be submitted through GitHub or via email to security@containerd.io. Reporters interested in participating in the discussion can provide their GitHub usernames for an invitation. Alternatively, they can opt to receive updates via email. If the vulnerability is accepted, a timeline for developing a patch, public disclosure, and patch release is established. In cases where an embargo period precedes public disclosure, an announcement is sent to the security announce mailing list, detailing the vulnerability scope, patch release date, and public disclosure date. Reporters are expected to engage in the discussion of the timeline and adhere to agreed-upon dates for public disclosure.

**- Incident Response**:

Defined procedures are in place for triaging reported vulnerabilities, assessing their severity and relevance. The confirmation process involves validating the reported vulnerability to determine its authenticity and impact. If the vulnerability is confirmed, the involved parties, including the reporter(s), are notified. A timeline for developing a patch and making updates available is determined. Depending on the embargo period, the vulnerability and patch release details are publicly disclosed using the security announce mailing list. Reporters are expected to comply with agreed-upon dates for public disclosure, ensuring a responsible and coordinated release of information. This process ensures a systematic and transparent approach to handling security issues, promoting responsible disclosure, and achieving timely resolution.

## Appendix


* Known Issues Over Time:
  
   There have been some problems in the past with the plugins that containerd has. Even though it is a feature, it has led to problems when the plugins are not correctly inputted.
    - https://github.com/containerd/containerd/pull/7347
    - https://github.com/containerd/containerd/pull/8056

  There is also a few issues with its ability to access resources. In attempt to keep least-privilege access, the dulling out when available can be problematic
    - https://github.com/containerd/containerd/issues/3351
    - https://www.cvedetails.com/cve/CVE-2023-25153/
    - https://www.cvedetails.com/cve/CVE-2022-31030/
    - https://www.cvedetails.com/cve/CVE-2022-23471/
    - https://www.cvedetails.com/cve/CVE-2021-32760/
  

* Record in catching issues in code review or automated testing:
  
   **Current Level: [![OpenSSF Best Practices](https://www.bestpractices.dev/projects/1271/badge)](https://www.bestpractices.dev/projects/1271)**
  
   The project achieved the following
     - Basics: 13/13 passed
     - Change Control: 9/9 passed
     - Reporting: 8/8 passed
     - Quality: 13/13 passed
     - Security: 16/16 passed
     - Analysis: 8/8 passed


* Case Studies:
  
   Demonstrates how Red Hat OpenShift, integrated with containerd, streamlines containerization adoption and simplifies Kubernetes management.
  
   https://swapnasagarpradhan.medium.com/install-a-kubernetes-cluster-on-rhel8-with-conatinerd-b48b9257877a

  Explores how containerd simplifies container management on Google Kubernetes Engine (GKE), Google Cloud's fully managed Kubernetes service.

  https://cloud.google.com/kubernetes-engine

  Delves into the integration of containerd with Amazon Elastic Container Service (ECS), Amazon Web Services' container orchestration service

  https://aws.amazon.com/blogs/containers/tag/containerd/

  Explores how containerd enables organizations to effectively manage containers on Azure Kubernetes Service (AKS), Microsoft Azure's managed Kubernetes service

  https://azure.microsoft.com/en-us/updates/generally-available-containerd-support-for-windows-in-aks/
  
* Related Projects / Vendors:

  https://www.docker.com/products/container-runtime/
  
  https://cri-o.io/
  
  https://humalect.com/blog/containerd-vs-docker/
  
  https://www.wallarm.com/cloud-native-products-101/containerd-vs-docker-what-is-the-difference-between-the-tools/
  
  
