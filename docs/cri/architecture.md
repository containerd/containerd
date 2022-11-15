# Architecture of The CRI Plugin
This document describes the architecture of the `cri` plugin for `containerd`.

This plugin is an implementation of Kubernetes [container runtime interface (CRI)](https://github.com/kubernetes/kubernetes/blob/master/staging/src/k8s.io/cri-api/pkg/apis/runtime/v1/api.proto). Containerd operates on the same node as the [Kubelet](https://kubernetes.io/docs/reference/command-line-tools-reference/kubelet/). The `cri` plugin inside containerd handles all CRI service requests from the Kubelet and uses containerd internals to manage containers and container images.

The `cri` plugin uses containerd to manage the full container lifecycle and all container images. As also shown below, `cri` manages pod networking via [CNI](https://github.com/containernetworking/cni) (another CNCF project).

![architecture](./architecture.png)

Let's use an example to demonstrate how the `cri` plugin works for the case when Kubelet creates a single-container pod:
* Kubelet calls the `cri` plugin, via the CRI runtime service API, to create a pod;
* `cri` creates the pod’s network namespace, and then configures it using CNI;
* `cri` uses containerd internal to create and start a special [pause container](https://www.ianlewis.org/en/almighty-pause-container) (the sandbox container) and put that container inside the pod’s cgroups and namespace (steps omitted for brevity);
* Kubelet subsequently calls the `cri` plugin, via the CRI image service API, to pull the application container image;
* `cri` further uses containerd to pull the image if the image is not present on the node;
* Kubelet then calls `cri`, via the CRI runtime service API, to create and start the application container inside the pod using the pulled container image;
* `cri` finally uses containerd internal to create the application container, put it inside the pod’s cgroups and namespace, then to start the pod’s new application container.
After these steps, a pod and its corresponding application container is created and running.
