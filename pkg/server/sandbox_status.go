/*
Copyright 2017 The Kubernetes Authors.

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
	"encoding/json"
	"fmt"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/errdefs"
	"github.com/cri-o/ocicni/pkg/ocicni"
	"github.com/golang/glog"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	sandboxstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/sandbox"
)

// PodSandboxStatus returns the status of the PodSandbox.
func (c *criContainerdService) PodSandboxStatus(ctx context.Context, r *runtime.PodSandboxStatusRequest) (*runtime.PodSandboxStatusResponse, error) {
	sandbox, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when try to find sandbox: %v", err)
	}

	task, err := sandbox.Container.Task(ctx, nil)
	if err != nil && !errdefs.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get sandbox container task: %v", err)
	}

	var pid uint32
	var processStatus containerd.ProcessStatus
	// If the sandbox container is running, treat it as READY.
	if task != nil {
		taskStatus, err := task.Status(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get task status: %v", err)
		}

		pid = task.Pid()
		processStatus = taskStatus.Status
	}

	ip, err := c.getIP(sandbox)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox ip: %v", err)
	}

	ctrInfo, err := sandbox.Container.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox container info: %v", err)
	}
	createdAt := ctrInfo.CreatedAt
	status := toCRISandboxStatus(sandbox.Metadata, processStatus, createdAt, ip)
	info, err := toCRISandboxInfo(ctx, sandbox, pid, processStatus, r.GetVerbose())
	if err != nil {
		return nil, fmt.Errorf("failed to get verbose sandbox container info: %v", err)
	}

	return &runtime.PodSandboxStatusResponse{
		Status: status,
		Info:   info,
	}, nil
}

func (c *criContainerdService) getIP(sandbox sandboxstore.Sandbox) (string, error) {
	config := sandbox.Config

	if config.GetLinux().GetSecurityContext().GetNamespaceOptions().GetHostNetwork() {
		// For sandboxes using the host network we are not
		// responsible for reporting the IP.
		return "", nil
	}

	if err := c.netPlugin.Status(); err != nil {
		// If the network is not ready then there is nothing to report.
		glog.V(4).Infof("getIP: unable to get sandbox %q network status: network plugin not ready.", sandbox.ID)
		return "", nil
	}

	// The network namespace has been closed.
	if sandbox.NetNS == nil || sandbox.NetNS.Closed() {
		return "", nil
	}

	podNetwork := ocicni.PodNetwork{
		Name:         config.GetMetadata().GetName(),
		Namespace:    config.GetMetadata().GetNamespace(),
		ID:           sandbox.ID,
		NetNS:        sandbox.NetNSPath,
		PortMappings: toCNIPortMappings(config.GetPortMappings()),
	}

	ip, err := c.netPlugin.GetPodNetworkStatus(podNetwork)
	if err == nil {
		return ip, nil
	}

	// Ignore the error on network status
	glog.V(4).Infof("getIP: failed to read sandbox %q IP from plugin: %v", sandbox.ID, err)
	return "", nil
}

// toCRISandboxStatus converts sandbox metadata into CRI pod sandbox status.
func toCRISandboxStatus(meta sandboxstore.Metadata, status containerd.ProcessStatus, createdAt time.Time, ip string) *runtime.PodSandboxStatus {
	// Set sandbox state to NOTREADY by default.
	state := runtime.PodSandboxState_SANDBOX_NOTREADY
	if status == containerd.Running {
		state = runtime.PodSandboxState_SANDBOX_READY
	}
	nsOpts := meta.Config.GetLinux().GetSecurityContext().GetNamespaceOptions()
	return &runtime.PodSandboxStatus{
		Id:        meta.ID,
		Metadata:  meta.Config.GetMetadata(),
		State:     state,
		CreatedAt: createdAt.UnixNano(),
		Network:   &runtime.PodSandboxNetworkStatus{Ip: ip},
		Linux: &runtime.LinuxPodSandboxStatus{
			Namespaces: &runtime.Namespace{
				Options: &runtime.NamespaceOption{
					HostNetwork: nsOpts.GetHostNetwork(),
					HostPid:     nsOpts.GetHostPid(),
					HostIpc:     nsOpts.GetHostIpc(),
				},
			},
		},
		Labels:      meta.Config.GetLabels(),
		Annotations: meta.Config.GetAnnotations(),
	}
}

// TODO (mikebrow): discuss predefining constants structures for some or all of these field names in CRI
type sandboxInfo struct {
	Pid         uint32                    `json:"pid"`
	Status      string                    `json:"processStatus"`
	NetNSClosed bool                      `json:"netNamespaceClosed"`
	Image       string                    `json:"image"`
	SnapshotKey string                    `json:"snapshotKey"`
	Snapshotter string                    `json:"snapshotter"`
	Config      *runtime.PodSandboxConfig `json:"config"`
	RuntimeSpec *runtimespec.Spec         `json:"runtimeSpec"`
}

// toCRISandboxInfo converts internal container object information to CRI sandbox status response info map.
func toCRISandboxInfo(ctx context.Context, sandbox sandboxstore.Sandbox,
	pid uint32, processStatus containerd.ProcessStatus, verbose bool) (map[string]string, error) {
	if !verbose {
		return nil, nil
	}

	si := &sandboxInfo{
		Pid:    pid,
		Status: string(processStatus),
		Config: sandbox.Config,
	}

	if si.Status == "" {
		// If processStatus is empty, it means that the task is deleted. Apply "deleted"
		// status which does not exist in containerd.
		si.Status = "deleted"
	}

	if sandbox.NetNSPath != "" {
		// Add network closed information if sandbox is not using host network.
		si.NetNSClosed = (sandbox.NetNS == nil || sandbox.NetNS.Closed())
	}

	container := sandbox.Container
	spec, err := container.Spec(ctx)
	if err == nil {
		si.RuntimeSpec = spec
	} else {
		glog.Errorf("Failed to get sandbox container %q runtime spec: %v", sandbox.ID, err)
	}

	ctrInfo, err := container.Info(ctx)
	if err == nil {
		// Do not use config.SandboxImage because the configuration might
		// be changed during restart. It may not reflect the actual image
		// used by the sandbox container.
		si.Image = ctrInfo.Image
		si.SnapshotKey = ctrInfo.SnapshotKey
		si.Snapshotter = ctrInfo.Snapshotter
	} else {
		glog.Errorf("Failed to get sandbox container %q info: %v", sandbox.ID, err)
	}

	infoBytes, err := json.Marshal(si)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal info %v: %v", si, err)
	}
	return map[string]string{
		"info": string(infoBytes),
	}, nil
}
