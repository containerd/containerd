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

package tracing

import (
	"go.opentelemetry.io/otel/attribute"
)

const (
	// Container attributes
	AttrContainerID      = "container.id"
	AttrContainerName    = "container.name"
	AttrContainerAttempt = "container.attempt"
	AttrContainerRuntime = "container.runtime"

	// Pod attributes
	AttrPodUID              = "pod.uid"
	AttrPodName             = "pod.name"
	AttrPodNamespace        = "pod.namespace"
	AttrContainerdNamespace = "containerd.namespace"

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

func AddStandardAttributes(span *Span, sandboxID, containerID, podName, podNS string) {
	if span == nil {
		return
	}
	kv := make([]attribute.KeyValue, 0, 4)
	if sandboxID != "" {
		kv = append(kv, Attribute(AttrSandboxID, sandboxID))
	}
	if containerID != "" {
		kv = append(kv, Attribute(AttrContainerID, containerID))
	}
	if podName != "" {
		kv = append(kv, Attribute(AttrPodName, podName))
	}
	if podNS != "" {
		kv = append(kv, Attribute(AttrPodNamespace, podNS))
	}
	if len(kv) > 0 {
		span.SetAttributes(kv...)
	}
}

func AddImageAttributes(span *Span, imageRef, digest string) {
	if span == nil {
		return
	}
	kv := make([]attribute.KeyValue, 0, 2)
	if imageRef != "" {
		kv = append(kv, Attribute(AttrImageRef, imageRef))
	}
	if digest != "" {
		kv = append(kv, Attribute(AttrImageDigest, digest))
	}
	if len(kv) > 0 {
		span.SetAttributes(kv...)
	}
}

func AddRuntimeAttributes(span *Span, runtimeName, snapshotter string) {
	if span == nil {
		return
	}
	kv := make([]attribute.KeyValue, 0, 2)
	if runtimeName != "" {
		kv = append(kv, Attribute(AttrContainerRuntime, runtimeName))
	}
	if snapshotter != "" {
		kv = append(kv, Attribute(AttrSnapshotter, snapshotter))
	}
	if len(kv) > 0 {
		span.SetAttributes(kv...)
	}
}
