/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package annotations

import (
	"maps"

	"github.com/containerd/containerd/v2/core/snapshots"
	customopts "github.com/containerd/containerd/v2/internal/cri/opts"
	"github.com/containerd/containerd/v2/pkg/oci"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// ContainerType values
// Following OCI annotations are used by katacontainers now.
// We'll switch to standard secure pod API after it is defined in CRI.
const (
	// SnapshotLabelPrefix is the label prefix used to pass labels to snapshotters.
	SnapshotLabelPrefix = snapshots.LabelSnapshotPrefix

	// ContainerTypeSandbox represents a pod sandbox container
	ContainerTypeSandbox = "sandbox"

	// ContainerTypeContainer represents a container running within a pod
	ContainerTypeContainer = "container"

	// ContainerType is the container type (sandbox or container) annotation
	ContainerType = "io.kubernetes.cri.container-type"

	// SandboxID is the sandbox ID annotation
	SandboxID = "io.kubernetes.cri.sandbox-id"

	// SandboxCPU annotations are based on the initial CPU configuration for the sandbox. This is calculated as the
	// sum of container CPU resources, optionally provided by Kubelet (introduced  in 1.23) as part of the PodSandboxConfig
	SandboxCPUPeriod = "io.kubernetes.cri.sandbox-cpu-period"
	SandboxCPUQuota  = "io.kubernetes.cri.sandbox-cpu-quota"
	SandboxCPUShares = "io.kubernetes.cri.sandbox-cpu-shares"

	// SandboxMemory is the initial amount of memory associated with this sandbox. This is calculated as the sum
	// of container memory, optionally provided by Kubelet (introduced in 1.23) as part of the PodSandboxConfig.
	SandboxMem = "io.kubernetes.cri.sandbox-memory"

	// SandboxLogDir is the pod log directory annotation.
	// If the sandbox needs to generate any log, it will put it into this directory.
	// Kubelet will be responsible for:
	// 1) Monitoring the disk usage of the log, and including it as part of the pod
	// ephemeral storage usage.
	// 2) Cleaning up the logs when the pod is deleted.
	// NOTE: Kubelet is not responsible for rotating the logs.
	SandboxLogDir = "io.kubernetes.cri.sandbox-log-directory"

	// UntrustedWorkload is the sandbox annotation for untrusted workload. Untrusted
	// workload can only run on dedicated runtime for untrusted workload.
	UntrustedWorkload = "io.kubernetes.cri.untrusted-workload"

	// SandboxNamespace is the name of the namespace of the sandbox (pod)
	SandboxNamespace = "io.kubernetes.cri.sandbox-namespace"

	// SandboxUID is the uid of the sandbox (pod) passed to CRI via RunPodSanbox,
	// this field is useful for linking the uid created by the CRI client (e.g. kubelet)
	// to the internal Sandbox.ID created by the containerd sandbox service
	SandboxUID = "io.kubernetes.cri.sandbox-uid"

	// SandboxName is the name of the sandbox (pod)
	SandboxName = "io.kubernetes.cri.sandbox-name"

	// ContainerName is the name of the container in the pod
	ContainerName = "io.kubernetes.cri.container-name"

	// ImageName is the name of the image used to create the container
	ImageName = "io.kubernetes.cri.image-name"

	// SandboxImageName is the name of the sandbox image
	SandboxImageName = "io.kubernetes.cri.podsandbox.image-name"

	// PodAnnotations are the annotations of the pod
	PodAnnotations = "io.kubernetes.cri.pod-annotations"

	// RuntimeHandler an experimental annotation key for getting runtime handler from pod annotations.
	// See https://github.com/containerd/containerd/issues/6657 and https://github.com/containerd/containerd/pull/6899 for details.
	// The value of this annotation should be the runtime for sandboxes.
	// e.g. for [plugins.cri.containerd.runtimes.runc] runtime config, this value should be runc
	//
	// Deprecated: Since cri-api v0.29.0 (Kubernetes 1.29+), kubelet passes the RuntimeHandler
	// in PullImageRequest. This annotation is only used as a fallback for older CRI clients
	// and will be removed in containerd 2.5.
	RuntimeHandler = "io.containerd.cri.runtime-handler"

	// WindowsHostProcess is used by hcsshim to identify windows pods that are running HostProcesses
	WindowsHostProcess = "microsoft.com/hostprocess-container"
)

// SnapshotLabel returns the snapshot label name for a CRI annotation.
func SnapshotLabel(name string) string {
	return SnapshotLabelPrefix + name
}

// DefaultCRIAnnotations are the default set of CRI annotations to
// pass to sandboxes and containers.
func DefaultCRIAnnotations(
	sandboxID string,
	containerName string,
	imageName string,
	config *runtime.PodSandboxConfig,
	sandbox bool,
) []oci.SpecOpts {
	opts := []oci.SpecOpts{
		customopts.WithAnnotation(SandboxID, sandboxID),
		customopts.WithAnnotation(SandboxNamespace, config.GetMetadata().GetNamespace()),
		customopts.WithAnnotation(SandboxUID, config.GetMetadata().GetUid()),
		customopts.WithAnnotation(SandboxName, config.GetMetadata().GetName()),
	}
	ctrType := ContainerTypeContainer
	if sandbox {
		ctrType = ContainerTypeSandbox
		// Sandbox log dir and sandbox image name get passed for sandboxes, the other metadata always
		// gets sent however.
		opts = append(
			opts,
			customopts.WithAnnotation(SandboxLogDir, config.GetLogDirectory()),
			customopts.WithAnnotation(SandboxImageName, imageName),
		)
	} else {
		// Image name and container name get passed for containers.
		opts = append(
			opts,
			customopts.WithAnnotation(ContainerName, containerName),
			customopts.WithAnnotation(ImageName, imageName),
		)
	}
	return append(opts, customopts.WithAnnotation(ContainerType, ctrType))
}

// DefaultCRISnapshotLabelsForSandbox returns the default set of CRI identity
// labels to pass to snapshotters for a sandbox.
func DefaultCRISnapshotLabelsForSandbox(
	sandboxID string,
	imageName string,
	config *runtime.PodSandboxConfig,
) map[string]string {
	labels := defaultCRISnapshotLabels(sandboxID, config, ContainerTypeSandbox)
	labels[SnapshotLabel(SandboxImageName)] = imageName
	return labels
}

// DefaultCRISnapshotLabelsForContainer returns the default set of CRI identity
// labels to pass to snapshotters for a container.
func DefaultCRISnapshotLabelsForContainer(
	sandboxID string,
	containerName string,
	imageName string,
	config *runtime.PodSandboxConfig,
) map[string]string {
	labels := defaultCRISnapshotLabels(sandboxID, config, ContainerTypeContainer)
	labels[SnapshotLabel(ContainerName)] = containerName
	labels[SnapshotLabel(ImageName)] = imageName
	return labels
}

func defaultCRISnapshotLabels(
	sandboxID string,
	config *runtime.PodSandboxConfig,
	containerType string,
) map[string]string {
	return map[string]string{
		SnapshotLabel(SandboxID):        sandboxID,
		SnapshotLabel(SandboxNamespace): config.GetMetadata().GetNamespace(),
		SnapshotLabel(SandboxUID):       config.GetMetadata().GetUid(),
		SnapshotLabel(SandboxName):      config.GetMetadata().GetName(),
		SnapshotLabel(ContainerType):    containerType,
	}
}

// MergeSnapshotLabels merges snapshot label maps in order. Later maps override
// earlier maps, allowing inherited or user-provided labels to take precedence
// over defaults.
func MergeSnapshotLabels(labels ...map[string]string) map[string]string {
	var total int
	for _, labelSet := range labels {
		total += len(labelSet)
	}
	merged := make(map[string]string, total)
	for _, labelSet := range labels {
		maps.Copy(merged, labelSet)
	}
	return merged
}
