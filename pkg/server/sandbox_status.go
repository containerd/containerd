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
	"fmt"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/errdefs"
	"github.com/cri-o/ocicni/pkg/ocicni"
	"github.com/golang/glog"
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

	// Set sandbox state to NOTREADY by default.
	state := runtime.PodSandboxState_SANDBOX_NOTREADY
	// If the sandbox container is running, treat it as READY.
	if task != nil {
		taskStatus, err := task.Status(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get task status: %v", err)
		}

		if taskStatus.Status == containerd.Running {
			state = runtime.PodSandboxState_SANDBOX_READY
		}
	}

	ip, err := c.getIP(sandbox)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox ip: %v", err)
	}

	info, err := sandbox.Container.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox container info: %v", err)
	}
	createdAt := info.CreatedAt
	status := toCRISandboxStatus(sandbox.Metadata, state, createdAt, ip)
	return &runtime.PodSandboxStatusResponse{Status: status}, nil
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
func toCRISandboxStatus(meta sandboxstore.Metadata, state runtime.PodSandboxState, createdAt time.Time, ip string) *runtime.PodSandboxStatus {
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
