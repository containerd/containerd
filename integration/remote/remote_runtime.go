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

/*
Copyright 2016 The Kubernetes Authors.

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

package remote

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/component-base/logs/logreduction"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	"k8s.io/klog/v2"
	utilexec "k8s.io/utils/exec"

	internalapi "github.com/containerd/containerd/v2/integration/cri-api/pkg/apis"
	"github.com/containerd/containerd/v2/integration/remote/util"
)

// RuntimeService is a gRPC implementation of internalapi.RuntimeService.
type RuntimeService struct {
	timeout       time.Duration
	runtimeClient runtimeapi.RuntimeServiceClient
	// Cache last per-container error message to reduce log spam
	logReduction *logreduction.LogReduction
}

const (
	// How frequently to report identical errors
	identicalErrorDelay = 1 * time.Minute
)

// NewRuntimeService creates a new internalapi.RuntimeService.
func NewRuntimeService(endpoint string, connectionTimeout time.Duration) (internalapi.RuntimeService, error) {
	klog.V(3).Infof("Connecting to runtime service %s", endpoint)
	addr, dialer, err := util.GetAddressAndDialer(endpoint)
	if err != nil {
		return nil, err
	}

	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMsgSize)),
	)
	if err != nil {
		klog.Errorf("Connect remote runtime %s failed: %v", addr, err)
		return nil, err
	}

	return &RuntimeService{
		timeout:       connectionTimeout,
		runtimeClient: runtimeapi.NewRuntimeServiceClient(conn),
		logReduction:  logreduction.NewLogReduction(identicalErrorDelay),
	}, nil
}

// Version returns the runtime name, runtime version and runtime API version.
func (r *RuntimeService) Version(apiVersion string, opts ...grpc.CallOption) (*runtimeapi.VersionResponse, error) {
	klog.V(10).Infof("[RuntimeService] Version (apiVersion=%v, timeout=%v)", apiVersion, r.timeout)

	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	typedVersion, err := r.runtimeClient.Version(ctx, &runtimeapi.VersionRequest{
		Version: apiVersion,
	}, opts...)
	if err != nil {
		klog.Errorf("Version from runtime service failed: %v", err)
		return nil, err
	}

	klog.V(10).Infof("[RuntimeService] Version Response (typedVersion=%v)", typedVersion)

	if typedVersion.Version == "" || typedVersion.RuntimeName == "" || typedVersion.RuntimeApiVersion == "" || typedVersion.RuntimeVersion == "" {
		return nil, fmt.Errorf("not all fields are set in VersionResponse (%q)", *typedVersion)
	}

	return typedVersion, err
}

// RunPodSandbox creates and starts a pod-level sandbox. Runtimes should ensure
// the sandbox is in ready state.
func (r *RuntimeService) RunPodSandbox(config *runtimeapi.PodSandboxConfig, runtimeHandler string, opts ...grpc.CallOption) (string, error) {
	// Use 2 times longer timeout for sandbox operation (4 mins by default)
	// TODO: Make the pod sandbox timeout configurable.
	timeout := r.timeout * 2

	klog.V(10).Infof("[RuntimeService] RunPodSandbox (config=%v, runtimeHandler=%v, timeout=%v)", config, runtimeHandler, timeout)

	ctx, cancel := getContextWithTimeout(timeout)
	defer cancel()

	resp, err := r.runtimeClient.RunPodSandbox(ctx, &runtimeapi.RunPodSandboxRequest{
		Config:         config,
		RuntimeHandler: runtimeHandler,
	}, opts...)
	if err != nil {
		klog.Errorf("RunPodSandbox from runtime service failed: %v", err)
		return "", err
	}

	if resp.PodSandboxId == "" {
		errorMessage := fmt.Sprintf("PodSandboxId is not set for sandbox %q", config.GetMetadata())
		klog.Errorf("RunPodSandbox failed: %s", errorMessage)
		return "", errors.New(errorMessage)
	}

	klog.V(10).Infof("[RuntimeService] RunPodSandbox Response (PodSandboxId=%v)", resp.PodSandboxId)

	return resp.PodSandboxId, nil
}

// StopPodSandbox stops the sandbox. If there are any running containers in the
// sandbox, they should be forced to termination.
func (r *RuntimeService) StopPodSandbox(podSandBoxID string, opts ...grpc.CallOption) error {
	klog.V(10).Infof("[RuntimeService] StopPodSandbox (podSandboxID=%v, timeout=%v)", podSandBoxID, r.timeout)

	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	_, err := r.runtimeClient.StopPodSandbox(ctx, &runtimeapi.StopPodSandboxRequest{
		PodSandboxId: podSandBoxID,
	}, opts...)
	if err != nil {
		klog.Errorf("StopPodSandbox %q from runtime service failed: %v", podSandBoxID, err)
		return err
	}

	klog.V(10).Infof("[RuntimeService] StopPodSandbox Response (podSandboxID=%v)", podSandBoxID)

	return nil
}

// RemovePodSandbox removes the sandbox. If there are any containers in the
// sandbox, they should be forcibly removed.
func (r *RuntimeService) RemovePodSandbox(podSandBoxID string, opts ...grpc.CallOption) error {
	klog.V(10).Infof("[RuntimeService] RemovePodSandbox (podSandboxID=%v, timeout=%v)", podSandBoxID, r.timeout)
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	_, err := r.runtimeClient.RemovePodSandbox(ctx, &runtimeapi.RemovePodSandboxRequest{
		PodSandboxId: podSandBoxID,
	}, opts...)
	if err != nil {
		klog.Errorf("RemovePodSandbox %q from runtime service failed: %v", podSandBoxID, err)
		return err
	}

	klog.V(10).Infof("[RuntimeService] RemovePodSandbox Response (podSandboxID=%v)", podSandBoxID)

	return nil
}

// PodSandboxStatus returns the status of the PodSandbox.
func (r *RuntimeService) PodSandboxStatus(podSandBoxID string, opts ...grpc.CallOption) (*runtimeapi.PodSandboxStatus, error) {
	klog.V(10).Infof("[RuntimeService] PodSandboxStatus (podSandboxID=%v, timeout=%v)", podSandBoxID, r.timeout)
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	resp, err := r.runtimeClient.PodSandboxStatus(ctx, &runtimeapi.PodSandboxStatusRequest{
		PodSandboxId: podSandBoxID,
	}, opts...)
	if err != nil {
		return nil, err
	}

	klog.V(10).Infof("[RuntimeService] PodSandboxStatus Response (podSandboxID=%v, status=%v)", podSandBoxID, resp.Status)

	if resp.Status != nil {
		if err := verifySandboxStatus(resp.Status); err != nil {
			return nil, err
		}
	}

	return resp.Status, nil
}

// ListPodSandbox returns a list of PodSandboxes.
func (r *RuntimeService) ListPodSandbox(filter *runtimeapi.PodSandboxFilter, opts ...grpc.CallOption) ([]*runtimeapi.PodSandbox, error) {
	klog.V(10).Infof("[RuntimeService] ListPodSandbox (filter=%v, timeout=%v)", filter, r.timeout)
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	resp, err := r.runtimeClient.ListPodSandbox(ctx, &runtimeapi.ListPodSandboxRequest{
		Filter: filter,
	}, opts...)
	if err != nil {
		klog.Errorf("ListPodSandbox with filter %+v from runtime service failed: %v", filter, err)
		return nil, err
	}

	klog.V(10).Infof("[RuntimeService] ListPodSandbox Response (filter=%v, items=%v)", filter, resp.Items)

	return resp.Items, nil
}

// CreateContainer creates a new container in the specified PodSandbox.
func (r *RuntimeService) CreateContainer(podSandBoxID string, config *runtimeapi.ContainerConfig, sandboxConfig *runtimeapi.PodSandboxConfig, opts ...grpc.CallOption) (string, error) {
	klog.V(10).Infof("[RuntimeService] CreateContainer (podSandBoxID=%v, timeout=%v)", podSandBoxID, r.timeout)
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	resp, err := r.runtimeClient.CreateContainer(ctx, &runtimeapi.CreateContainerRequest{
		PodSandboxId:  podSandBoxID,
		Config:        config,
		SandboxConfig: sandboxConfig,
	}, opts...)
	if err != nil {
		klog.Errorf("CreateContainer in sandbox %q from runtime service failed: %v", podSandBoxID, err)
		return "", err
	}

	klog.V(10).Infof("[RuntimeService] CreateContainer (podSandBoxID=%v, ContainerId=%v)", podSandBoxID, resp.ContainerId)
	if resp.ContainerId == "" {
		errorMessage := fmt.Sprintf("ContainerId is not set for container %q", config.GetMetadata())
		klog.Errorf("CreateContainer failed: %s", errorMessage)
		return "", errors.New(errorMessage)
	}

	return resp.ContainerId, nil
}

// StartContainer starts the container.
func (r *RuntimeService) StartContainer(containerID string, opts ...grpc.CallOption) error {
	klog.V(10).Infof("[RuntimeService] StartContainer (containerID=%v, timeout=%v)", containerID, r.timeout)
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	_, err := r.runtimeClient.StartContainer(ctx, &runtimeapi.StartContainerRequest{
		ContainerId: containerID,
	}, opts...)
	if err != nil {
		klog.Errorf("StartContainer %q from runtime service failed: %v", containerID, err)
		return err
	}
	klog.V(10).Infof("[RuntimeService] StartContainer Response (containerID=%v)", containerID)

	return nil
}

// StopContainer stops a running container with a grace period (i.e., timeout).
func (r *RuntimeService) StopContainer(containerID string, timeout int64, opts ...grpc.CallOption) error {
	klog.V(10).Infof("[RuntimeService] StopContainer (containerID=%v, timeout=%v)", containerID, timeout)
	// Use timeout + default timeout (2 minutes) as timeout to leave extra time
	// for SIGKILL container and request latency.
	t := r.timeout + time.Duration(timeout)*time.Second
	ctx, cancel := getContextWithTimeout(t)
	defer cancel()

	r.logReduction.ClearID(containerID)
	_, err := r.runtimeClient.StopContainer(ctx, &runtimeapi.StopContainerRequest{
		ContainerId: containerID,
		Timeout:     timeout,
	}, opts...)
	if err != nil {
		klog.Errorf("StopContainer %q from runtime service failed: %v", containerID, err)
		return err
	}
	klog.V(10).Infof("[RuntimeService] StopContainer Response (containerID=%v)", containerID)

	return nil
}

// RemoveContainer removes the container. If the container is running, the container
// should be forced to removal.
func (r *RuntimeService) RemoveContainer(containerID string, opts ...grpc.CallOption) error {
	klog.V(10).Infof("[RuntimeService] RemoveContainer (containerID=%v, timeout=%v)", containerID, r.timeout)
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	r.logReduction.ClearID(containerID)
	_, err := r.runtimeClient.RemoveContainer(ctx, &runtimeapi.RemoveContainerRequest{
		ContainerId: containerID,
	}, opts...)
	if err != nil {
		klog.Errorf("RemoveContainer %q from runtime service failed: %v", containerID, err)
		return err
	}
	klog.V(10).Infof("[RuntimeService] RemoveContainer Response (containerID=%v)", containerID)

	return nil
}

// ListContainers lists containers by filters.
func (r *RuntimeService) ListContainers(filter *runtimeapi.ContainerFilter, opts ...grpc.CallOption) ([]*runtimeapi.Container, error) {
	klog.V(10).Infof("[RuntimeService] ListContainers (filter=%v, timeout=%v)", filter, r.timeout)
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	resp, err := r.runtimeClient.ListContainers(ctx, &runtimeapi.ListContainersRequest{
		Filter: filter,
	}, opts...)
	if err != nil {
		klog.Errorf("ListContainers with filter %+v from runtime service failed: %v", filter, err)
		return nil, err
	}
	klog.V(10).Infof("[RuntimeService] ListContainers Response (filter=%v, containers=%v)", filter, resp.Containers)

	return resp.Containers, nil
}

// ContainerStatus returns the container status.
func (r *RuntimeService) ContainerStatus(containerID string, opts ...grpc.CallOption) (*runtimeapi.ContainerStatus, error) {
	klog.V(10).Infof("[RuntimeService] ContainerStatus (containerID=%v, timeout=%v)", containerID, r.timeout)
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	resp, err := r.runtimeClient.ContainerStatus(ctx, &runtimeapi.ContainerStatusRequest{
		ContainerId: containerID,
	}, opts...)
	if err != nil {
		// Don't spam the log with endless messages about the same failure.
		if r.logReduction.ShouldMessageBePrinted(err.Error(), containerID) {
			klog.Errorf("ContainerStatus %q from runtime service failed: %v", containerID, err)
		}
		return nil, err
	}
	r.logReduction.ClearID(containerID)
	klog.V(10).Infof("[RuntimeService] ContainerStatus Response (containerID=%v, status=%v)", containerID, resp.Status)

	if resp.Status != nil {
		if err := verifyContainerStatus(resp.Status); err != nil {
			klog.Errorf("ContainerStatus of %q failed: %v", containerID, err)
			return nil, err
		}
	}

	return resp.Status, nil
}

// UpdateContainerResources updates a containers resource config
func (r *RuntimeService) UpdateContainerResources(containerID string, resources *runtimeapi.LinuxContainerResources, windowsResources *runtimeapi.WindowsContainerResources, opts ...grpc.CallOption) error {
	klog.V(10).Infof("[RuntimeService] UpdateContainerResources (containerID=%v, timeout=%v)", containerID, r.timeout)
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	_, err := r.runtimeClient.UpdateContainerResources(ctx, &runtimeapi.UpdateContainerResourcesRequest{
		ContainerId: containerID,
		Linux:       resources,
		Windows:     windowsResources,
	}, opts...)
	if err != nil {
		klog.Errorf("UpdateContainerResources %q from runtime service failed: %v", containerID, err)
		return err
	}
	klog.V(10).Infof("[RuntimeService] UpdateContainerResources Response (containerID=%v)", containerID)

	return nil
}

// ExecSync executes a command in the container, and returns the stdout output.
// If command exits with a non-zero exit code, an error is returned.
func (r *RuntimeService) ExecSync(containerID string, cmd []string, timeout time.Duration, opts ...grpc.CallOption) (stdout []byte, stderr []byte, err error) {
	klog.V(10).Infof("[RuntimeService] ExecSync (containerID=%v, timeout=%v)", containerID, timeout)
	// Do not set timeout when timeout is 0.
	var ctx context.Context
	var cancel context.CancelFunc
	if timeout != 0 {
		// Use timeout + default timeout (2 minutes) as timeout to leave some time for
		// the runtime to do cleanup.
		ctx, cancel = getContextWithTimeout(r.timeout + timeout)
	} else {
		ctx, cancel = getContextWithCancel()
	}
	defer cancel()

	timeoutSeconds := int64(timeout.Seconds())
	req := &runtimeapi.ExecSyncRequest{
		ContainerId: containerID,
		Cmd:         cmd,
		Timeout:     timeoutSeconds,
	}
	resp, err := r.runtimeClient.ExecSync(ctx, req, opts...)
	if err != nil {
		klog.Errorf("ExecSync %s '%s' from runtime service failed: %v", containerID, strings.Join(cmd, " "), err)
		return nil, nil, err
	}

	klog.V(10).Infof("[RuntimeService] ExecSync Response (containerID=%v, ExitCode=%v)", containerID, resp.ExitCode)
	err = nil
	if resp.ExitCode != 0 {
		err = utilexec.CodeExitError{
			Err:  fmt.Errorf("command '%s' exited with %d: %s", strings.Join(cmd, " "), resp.ExitCode, resp.Stderr),
			Code: int(resp.ExitCode),
		}
	}

	return resp.Stdout, resp.Stderr, err
}

// Exec prepares a streaming endpoint to execute a command in the container, and returns the address.
func (r *RuntimeService) Exec(req *runtimeapi.ExecRequest, opts ...grpc.CallOption) (*runtimeapi.ExecResponse, error) {
	klog.V(10).Infof("[RuntimeService] Exec (timeout=%v)", r.timeout)
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	resp, err := r.runtimeClient.Exec(ctx, req, opts...)
	if err != nil {
		klog.Errorf("Exec %s '%s' from runtime service failed: %v", req.ContainerId, strings.Join(req.Cmd, " "), err)
		return nil, err
	}
	klog.V(10).Info("[RuntimeService] Exec Response")

	if resp.Url == "" {
		errorMessage := "URL is not set"
		klog.Errorf("Exec failed: %s", errorMessage)
		return nil, errors.New(errorMessage)
	}

	return resp, nil
}

// Attach prepares a streaming endpoint to attach to a running container, and returns the address.
func (r *RuntimeService) Attach(req *runtimeapi.AttachRequest, opts ...grpc.CallOption) (*runtimeapi.AttachResponse, error) {
	klog.V(10).Infof("[RuntimeService] Attach (containerId=%v, timeout=%v)", req.ContainerId, r.timeout)
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	resp, err := r.runtimeClient.Attach(ctx, req, opts...)
	if err != nil {
		klog.Errorf("Attach %s from runtime service failed: %v", req.ContainerId, err)
		return nil, err
	}
	klog.V(10).Infof("[RuntimeService] Attach Response (containerId=%v)", req.ContainerId)

	if resp.Url == "" {
		errorMessage := "URL is not set"
		klog.Errorf("Attach failed: %s", errorMessage)
		return nil, errors.New(errorMessage)
	}
	return resp, nil
}

// PortForward prepares a streaming endpoint to forward ports from a PodSandbox, and returns the address.
func (r *RuntimeService) PortForward(req *runtimeapi.PortForwardRequest, opts ...grpc.CallOption) (*runtimeapi.PortForwardResponse, error) {
	klog.V(10).Infof("[RuntimeService] PortForward (podSandboxID=%v, port=%v, timeout=%v)", req.PodSandboxId, req.Port, r.timeout)
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	resp, err := r.runtimeClient.PortForward(ctx, req, opts...)
	if err != nil {
		klog.Errorf("PortForward %s from runtime service failed: %v", req.PodSandboxId, err)
		return nil, err
	}
	klog.V(10).Infof("[RuntimeService] PortForward Response (podSandboxID=%v)", req.PodSandboxId)

	if resp.Url == "" {
		errorMessage := "URL is not set"
		klog.Errorf("PortForward failed: %s", errorMessage)
		return nil, errors.New(errorMessage)
	}

	return resp, nil
}

// UpdateRuntimeConfig updates the config of a runtime service. The only
// update payload currently supported is the pod CIDR assigned to a node,
// and the runtime service just proxies it down to the network plugin.
func (r *RuntimeService) UpdateRuntimeConfig(runtimeConfig *runtimeapi.RuntimeConfig, opts ...grpc.CallOption) error {
	klog.V(10).Infof("[RuntimeService] UpdateRuntimeConfig (runtimeConfig=%v, timeout=%v)", runtimeConfig, r.timeout)
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	// Response doesn't contain anything of interest. This translates to an
	// Event notification to the network plugin, which can't fail, so we're
	// really looking to surface destination unreachable.
	_, err := r.runtimeClient.UpdateRuntimeConfig(ctx, &runtimeapi.UpdateRuntimeConfigRequest{
		RuntimeConfig: runtimeConfig,
	}, opts...)

	if err != nil {
		return err
	}
	klog.V(10).Infof("[RuntimeService] UpdateRuntimeConfig Response (runtimeConfig=%v)", runtimeConfig)

	return nil
}

// Status returns the status of the runtime.
func (r *RuntimeService) Status(opts ...grpc.CallOption) (*runtimeapi.StatusResponse, error) {
	klog.V(10).Infof("[RuntimeService] Status (timeout=%v)", r.timeout)
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	resp, err := r.runtimeClient.Status(ctx, &runtimeapi.StatusRequest{Verbose: true}, opts...)
	if err != nil {
		klog.Errorf("Status from runtime service failed: %v", err)
		return nil, err
	}

	klog.V(10).Infof("[RuntimeService] Status Response (status=%v)", resp.Status)

	if resp.Status == nil || len(resp.Status.Conditions) < 2 {
		errorMessage := "RuntimeReady or NetworkReady condition are not set"
		klog.Errorf("Status failed: %s", errorMessage)
		return nil, errors.New(errorMessage)
	}

	return resp, nil
}

// RuntimeConfig returns the CgroupDriver of the runtime.
func (r *RuntimeService) RuntimeConfig(in *runtimeapi.RuntimeConfigRequest, opts ...grpc.CallOption) (*runtimeapi.RuntimeConfigResponse, error) {
	klog.V(10).Infof("[RuntimeService] RuntimeConfig (timeout=%v)", r.timeout)
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()
	runtimeConfig, err := r.runtimeClient.RuntimeConfig(ctx, in)
	return runtimeConfig, err
}

// ContainerStats returns the stats of the container.
func (r *RuntimeService) ContainerStats(containerID string, opts ...grpc.CallOption) (*runtimeapi.ContainerStats, error) {
	klog.V(10).Infof("[RuntimeService] ContainerStats (containerID=%v, timeout=%v)", containerID, r.timeout)
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	resp, err := r.runtimeClient.ContainerStats(ctx, &runtimeapi.ContainerStatsRequest{
		ContainerId: containerID,
	}, opts...)
	if err != nil {
		if r.logReduction.ShouldMessageBePrinted(err.Error(), containerID) {
			klog.Errorf("ContainerStats %q from runtime service failed: %v", containerID, err)
		}
		return nil, err
	}
	r.logReduction.ClearID(containerID)
	klog.V(10).Infof("[RuntimeService] ContainerStats Response (containerID=%v, stats=%v)", containerID, resp.GetStats())

	return resp.GetStats(), nil
}

// ListContainerStats lists all container stats given the provided filter
func (r *RuntimeService) ListContainerStats(filter *runtimeapi.ContainerStatsFilter, opts ...grpc.CallOption) ([]*runtimeapi.ContainerStats, error) {
	klog.V(10).Infof("[RuntimeService] ListContainerStats (filter=%v)", filter)
	// Do not set timeout, because writable layer stats collection takes time.
	// TODO(random-liu): Should we assume runtime should cache the result, and set timeout here?
	ctx, cancel := getContextWithCancel()
	defer cancel()

	resp, err := r.runtimeClient.ListContainerStats(ctx, &runtimeapi.ListContainerStatsRequest{
		Filter: filter,
	}, opts...)
	if err != nil {
		klog.Errorf("ListContainerStats with filter %+v from runtime service failed: %v", filter, err)
		return nil, err
	}
	klog.V(10).Infof("[RuntimeService] ListContainerStats Response (filter=%v, stats=%v)", filter, resp.GetStats())

	return resp.GetStats(), nil
}

// ReopenContainerLog reopens the container log for the given container ID
func (r *RuntimeService) ReopenContainerLog(containerID string, opts ...grpc.CallOption) error {
	klog.V(10).Infof("[RuntimeService] ReopenContainerLog (containerID=%v, timeout=%v)", containerID, r.timeout)
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	_, err := r.runtimeClient.ReopenContainerLog(ctx, &runtimeapi.ReopenContainerLogRequest{
		ContainerId: containerID,
	}, opts...)
	if err != nil {
		klog.Errorf("ReopenContainerLog %q from runtime service failed: %v", containerID, err)
		return err
	}

	klog.V(10).Infof("[RuntimeService] ReopenContainerLog Response (containerID=%v)", containerID)
	return nil
}

// GetContainerEvents returns a GRPC client to stream container events
func (r *RuntimeService) GetContainerEvents(ctx context.Context, request *runtimeapi.GetEventsRequest, opts ...grpc.CallOption) (runtimeapi.RuntimeService_GetContainerEventsClient, error) {
	klog.V(10).Infof("[RuntimeService] GetContainerEvents (timeout=%v)", r.timeout)

	client, err := r.runtimeClient.GetContainerEvents(ctx, request, opts...)
	if err != nil {
		klog.Errorf("GetContainerEvents from runtime service failed: %v", err)
		return nil, err
	}
	return client, nil
}
