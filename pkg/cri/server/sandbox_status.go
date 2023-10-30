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
	"fmt"
	"time"

	"github.com/containerd/containerd/v2/errdefs"
	sandboxstore "github.com/containerd/containerd/v2/pkg/cri/store/sandbox"
	"github.com/containerd/go-cni"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
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

	controller, err := c.getSandboxController(sandbox.Config, sandbox.RuntimeHandler)
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
	RuntimeSpec    *runtimespec.Spec         `json:"runtimeSpec"`
	CNIResult      *cni.Result               `json:"cniResult"`
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
				Options: &runtime.NamespaceOption{
					Network: nsOpts.GetNetwork(),
					Pid:     nsOpts.GetPid(),
					Ipc:     nsOpts.GetIpc(),
				},
			},
		},
		Labels:         meta.Config.GetLabels(),
		Annotations:    meta.Config.GetAnnotations(),
		RuntimeHandler: meta.RuntimeHandler,
	}
}
