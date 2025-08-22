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

package server

import (
	"github.com/containerd/containerd/v2/pkg/tracing"
	"go.opentelemetry.io/otel/attribute"
)

const (
	// Container attributes
	AttrContainerID      = "container.id"
	AttrContainerName    = "container.name"
	AttrContainerAttempt = "container.attempt"
	AttrContainerRuntime = "container.runtime"

	// Pod attributes
	AttrPodUID       = "pod.uid"
	AttrPodName      = "pod.name"
	AttrPodNamespace = "pod.namespace"

	// Image attributes
	AttrImageRef    = "container.image.ref"
	AttrImageDigest = "image.digest"

	// Infrastructure attributes
	AttrSnapshotter = "snapshotter.name"
	AttrNodeName    = "node.name"

	// Operation attributes
	AttrOperationType  = "operation.type"
	AttrOperationPhase = "operation.phase"
	AttrErrorCode      = "error.code"
	AttrExitCode       = "container.exit_code"

	// Sandbox attributes
	AttrSandboxID   = "sandbox.id"
	AttrSandboxName = "sandbox.name"
)

const (
	SpanCRICreateContainer = "cri.create_container"
	SpanCRIStartContainer  = "cri.start_container"
	SpanCRIStopContainer   = "cri.stop_container"
	SpanCRIRemoveContainer = "cri.remove_container"
	SpanCRIPullImage       = "cri.pull_image"
	SpanCRICreateSandbox   = "cri.create_sandbox"
	SpanCRIStartSandbox    = "cri.start_sandbox"
	SpanCRIStopSandbox     = "cri.stop_sandbox"
	SpanCRIRemoveSandbox   = "cri.remove_sandbox"
	SpanCNISetup           = "cni.setup_pod_network"
	SpanCNITeardown        = "cni.teardown_pod_network"
	SpanUsernsCreate       = "userns.create_mapping"
	SpanSnapshotPrepare    = "snapshot.prepare"
	SpanSnapshotCommit     = "snapshot.commit"
	SpanMountSnapshot      = "mount.snapshot"
	SpanUnmountSnapshot    = "mount.unmount_snapshot"
	SpanRuntimeTaskCreate  = "runtime.task_create"
	SpanRuntimeTaskStart   = "runtime.task_start"
	SpanRuntimeTaskKill    = "runtime.task_kill"
)

// AddCRIStandardAttrs attaches common CRI attributes to the span.
func AddCRIStandardAttrs(span *tracing.Span, sandboxID, containerID, podName, podNamespace string) {
	if span == nil {
		return
	}
	kv := make([]attribute.KeyValue, 0, 4)
	if sandboxID != "" {
		kv = append(kv, tracing.Attribute(AttrSandboxID, sandboxID))
	}
	if containerID != "" {
		kv = append(kv, tracing.Attribute(AttrContainerID, containerID))
	}
	if podName != "" {
		kv = append(kv, tracing.Attribute(AttrPodName, podName))
	}
	if podNamespace != "" {
		kv = append(kv, tracing.Attribute(AttrPodNamespace, podNamespace))
	}
	if len(kv) > 0 {
		span.SetAttributes(kv...)
	}
}
