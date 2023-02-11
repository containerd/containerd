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

package podsandbox

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	"github.com/containerd/containerd/sandbox"
	"github.com/containerd/go-cni"
	"github.com/containerd/typeurl/v2"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// SandboxInfo is extra information for sandbox.
// TODO (mikebrow): discuss predefining constants structures for some or all of these field names in CRI
type SandboxInfo struct {
	Pid         uint32 `json:"pid"`
	Status      string `json:"processStatus"`
	NetNSClosed bool   `json:"netNamespaceClosed"`
	Image       string `json:"image"`
	SnapshotKey string `json:"snapshotKey"`
	Snapshotter string `json:"snapshotter"`
	// Note: a new field `RuntimeHandler` has been added into the CRI PodSandboxStatus struct, and
	// should be set. This `RuntimeHandler` field will be deprecated after containerd 1.3 (tracked
	// in https://github.com/containerd/cri/issues/1064).
	RuntimeHandler string                    `json:"runtimeHandler"` // see the Note above
	RuntimeType    string                    `json:"runtimeType"`
	RuntimeOptions interface{}               `json:"runtimeOptions"`
	Config         *runtime.PodSandboxConfig `json:"config"`
	// Note: RuntimeSpec may not be populated if the sandbox has not been fully created.
	RuntimeSpec *runtimespec.Spec      `json:"runtimeSpec"`
	CNIResult   *cni.Result            `json:"cniResult"`
	Metadata    *sandboxstore.Metadata `json:"sandboxMetadata"`
}

func (c *Controller) Status(ctx context.Context, sandboxID string, verbose bool) (sandbox.ControllerStatus, error) {
	sb, err := c.sandboxStore.Get(sandboxID)
	if err != nil {
		return sandbox.ControllerStatus{}, fmt.Errorf("an error occurred while trying to find sandbox %q: %w",
			sandboxID, err)
	}

	status := sb.Status.Get()
	cstatus := sandbox.ControllerStatus{
		SandboxID: sandboxID,
		Pid:       status.Pid,
		State:     status.State.String(),
		CreatedAt: status.CreatedAt,
		Extra:     nil,
	}

	if !status.ExitedAt.IsZero() {
		cstatus.ExitedAt = status.ExitedAt
	}

	if verbose {
		info, err := toCRISandboxInfo(ctx, sb)
		if err != nil {
			return sandbox.ControllerStatus{}, err
		}

		cstatus.Info = info
	}

	return cstatus, nil
}

// toCRISandboxInfo converts internal container object information to CRI sandbox status response info map.
func toCRISandboxInfo(ctx context.Context, sandbox sandboxstore.Sandbox) (map[string]string, error) {
	si := &SandboxInfo{
		Pid:            sandbox.Status.Get().Pid,
		Config:         sandbox.Config,
		RuntimeHandler: sandbox.RuntimeHandler,
		CNIResult:      sandbox.CNIResult,
	}

	if container := sandbox.Container; container != nil {
		task, err := container.Task(ctx, nil)
		if err != nil && !errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get sandbox container task: %w", err)
		}

		var processStatus containerd.ProcessStatus
		if task != nil {
			if taskStatus, err := task.Status(ctx); err != nil {
				if !errdefs.IsNotFound(err) {
					return nil, fmt.Errorf("failed to get task status: %w", err)
				}
				processStatus = containerd.Unknown
			} else {
				processStatus = taskStatus.Status
			}
		}
		si.Status = string(processStatus)

		spec, err := container.Spec(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get sandbox container runtime spec: %w", err)
		}
		si.RuntimeSpec = spec

		ctrInfo, err := container.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get sandbox container info: %w", err)
		}
		// Do not use config.SandboxImage because the configuration might
		// be changed during restart. It may not reflect the actual image
		// used by the sandbox container.
		si.Image = ctrInfo.Image
		si.SnapshotKey = ctrInfo.SnapshotKey
		si.Snapshotter = ctrInfo.Snapshotter

		runtimeOptions, err := getRuntimeOptions(ctrInfo)
		if err != nil {
			return nil, fmt.Errorf("failed to get runtime options: %w", err)
		}

		si.RuntimeType = ctrInfo.Runtime.Name
		si.RuntimeOptions = runtimeOptions
	}

	if si.Status == "" {
		// If processStatus is empty, it means that the task is deleted. Apply "deleted"
		// status which does not exist in containerd.
		si.Status = "deleted"
	}

	if sandbox.NetNS != nil {
		// Add network closed information if sandbox is not using host network.
		closed, err := sandbox.NetNS.Closed()
		if err != nil {
			return nil, fmt.Errorf("failed to check network namespace closed: %w", err)
		}
		si.NetNSClosed = closed
	}

	si.Metadata = &sandbox.Metadata

	infoBytes, err := json.Marshal(si)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal info %v: %w", si, err)
	}

	return map[string]string{
		"info": string(infoBytes),
	}, nil
}

// getRuntimeOptions get runtime options from container metadata.
func getRuntimeOptions(c containers.Container) (interface{}, error) {
	from := c.Runtime.Options
	if from == nil || from.GetValue() == nil {
		return nil, nil
	}
	opts, err := typeurl.UnmarshalAny(from)
	if err != nil {
		return nil, err
	}
	return opts, nil
}
