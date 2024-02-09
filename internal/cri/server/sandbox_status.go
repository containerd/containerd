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
	"context"
	"encoding/json"
	"fmt"
	"time"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/types"
	"github.com/containerd/errdefs"
)

// PodSandboxStatus returns the status of the PodSandbox.
func (c *criService) PodSandboxStatus(ctx context.Context, r *runtime.PodSandboxStatusRequest) (*runtime.PodSandboxStatusResponse, error) {
	sandbox, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when try to find sandbox: %w", err)
	}

	ip, additionalIPs, err := c.getIPs(sandbox)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox ip: %w", err)
	}

	controller, err := c.sandboxService.SandboxController(sandbox.Config, sandbox.RuntimeHandler)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox controller: %w", err)
	}

	var (
		createdAt time.Time
		state     string
		info      map[string]string
	)
	cstatus, err := controller.Status(ctx, sandbox.ID, r.GetVerbose())
	if err != nil {
		// If the shim died unexpectedly (segfault etc.) let's set the state as
		// NOTREADY and not just error out to make k8s and clients like crictl
		// happy. If we get back ErrNotFound from controller.Status above while
		// we're using the shim-mode controller, this is a decent indicator it
		// exited unexpectedly. We can use the fact that we successfully retrieved
		// the sandbox object from the store above to tell that this is true, otherwise
		// if we followed the normal k8s convention of StopPodSandbox -> RemovePodSandbox,
		// we wouldn't have that object in the store anymore.
		if !errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("failed to query controller status: %w", err)
		}
		state = runtime.PodSandboxState_SANDBOX_NOTREADY.String()
		if r.GetVerbose() {
			info, err = toDeletedCRISandboxInfo(sandbox)
			if err != nil {
				return nil, err
			}
		}
	} else {
		state = cstatus.State
		createdAt = cstatus.CreatedAt
		info = cstatus.Info
	}

	status := toCRISandboxStatus(sandbox.Metadata, state, createdAt, ip, additionalIPs)
	if status.GetCreatedAt() == 0 {
		// CRI doesn't allow CreatedAt == 0.
		sandboxInfo, err := c.client.SandboxStore().Get(ctx, sandbox.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to get sandbox %q from metadata store: %w", sandbox.ID, err)
		}
		status.CreatedAt = sandboxInfo.CreatedAt.UnixNano()
	}

	return &runtime.PodSandboxStatusResponse{
		Status: status,
		Info:   info,
	}, nil
}

func (c *criService) getIPs(sandbox sandboxstore.Sandbox) (string, []string, error) {
	config := sandbox.Config

	// For sandboxes using the node network we are not
	// responsible for reporting the IP.
	if hostNetwork(config) {
		return "", nil, nil
	}

	if closed, err := sandbox.NetNS.Closed(); err != nil {
		return "", nil, fmt.Errorf("check network namespace closed: %w", err)
	} else if closed {
		return "", nil, nil
	}

	return sandbox.IP, sandbox.AdditionalIPs, nil
}

// toCRISandboxStatus converts sandbox metadata into CRI pod sandbox status.
func toCRISandboxStatus(meta sandboxstore.Metadata, status string, createdAt time.Time, ip string, additionalIPs []string) *runtime.PodSandboxStatus {
	// Set sandbox state to NOTREADY by default.
	state := runtime.PodSandboxState_SANDBOX_NOTREADY
	if value, ok := runtime.PodSandboxState_value[status]; ok {
		state = runtime.PodSandboxState(value)
	}
	nsOpts := meta.Config.GetLinux().GetSecurityContext().GetNamespaceOptions()
	var ips []*runtime.PodIP
	for _, additionalIP := range additionalIPs {
		ips = append(ips, &runtime.PodIP{Ip: additionalIP})
	}
	return &runtime.PodSandboxStatus{
		Id:        meta.ID,
		Metadata:  meta.Config.GetMetadata(),
		State:     state,
		CreatedAt: createdAt.UnixNano(),
		Network: &runtime.PodSandboxNetworkStatus{
			Ip:            ip,
			AdditionalIps: ips,
		},
		Linux: &runtime.LinuxPodSandboxStatus{
			Namespaces: &runtime.Namespace{
				Options: nsOpts,
			},
		},
		Labels:         meta.Config.GetLabels(),
		Annotations:    meta.Config.GetAnnotations(),
		RuntimeHandler: meta.RuntimeHandler,
	}
}

// toDeletedCRISandboxInfo converts cached sandbox to CRI sandbox status response info map.
// In most cases, controller.Status() with verbose=true should have SandboxInfo in the return,
// but if controller.Status() returns a NotFound error,
// we should fallback to get SandboxInfo from cached sandbox itself.
func toDeletedCRISandboxInfo(sandbox sandboxstore.Sandbox) (map[string]string, error) {
	si := &types.SandboxInfo{
		Pid:            sandbox.Status.Get().Pid,
		Config:         sandbox.Config,
		RuntimeHandler: sandbox.RuntimeHandler,
		CNIResult:      sandbox.CNIResult,
	}

	// If processStatus is empty, it means that the task is deleted. Apply "deleted"
	// status which does not exist in containerd.
	si.Status = "deleted"

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
