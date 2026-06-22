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
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	upstreamapi "k8s.io/cri-api/pkg/apis"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	upstreamcri "k8s.io/cri-client/pkg"
	criutil "k8s.io/cri-client/pkg/util"
)

const maxMsgSize = 1024 * 1024 * 16

// RuntimeService adapts the upstream CRI client to the legacy integration
// interface used by containerd's integration tests.
type RuntimeService struct {
	runtimeService upstreamapi.RuntimeService
	runtimeClient  runtimeapi.RuntimeServiceClient
	runtimeConn    *grpc.ClientConn
}

// NewRuntimeService creates a legacy-style CRI runtime client backed by the
// upstream Kubernetes CRI client.
func NewRuntimeService(endpoint string, connectionTimeout time.Duration) (*RuntimeService, error) {
	runtimeService, err := upstreamcri.NewRemoteRuntimeService(context.Background(), endpoint, connectionTimeout, nil, false)
	if err != nil {
		return nil, err
	}

	runtimeConn, err := newRuntimeClientConn(endpoint)
	if err != nil {
		_ = runtimeService.Close(context.Background())
		return nil, err
	}

	return &RuntimeService{
		runtimeService: runtimeService,
		runtimeClient:  runtimeapi.NewRuntimeServiceClient(runtimeConn),
		runtimeConn:    runtimeConn,
	}, nil
}

func newRuntimeClientConn(endpoint string) (*grpc.ClientConn, error) {
	addr, dialer, err := criutil.GetAddressAndDialer(endpoint)
	if err != nil {
		return nil, err
	}

	return grpc.NewClient(clientTargetForAddress(addr),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithAuthority("localhost"),
		grpc.WithContextDialer(dialer),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMsgSize)),
	)
}

func clientTargetForAddress(addr string) string {
	// grpc.NewClient defaults to the DNS resolver. Use the passthrough resolver
	// for socket paths so the custom dialer receives the raw endpoint string.
	if strings.HasPrefix(addr, "/") {
		return "passthrough:///" + addr
	}
	return addr
}

func (r *RuntimeService) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}

	var err error
	if r.runtimeService != nil {
		err = r.runtimeService.Close(ctx)
	}
	if r.runtimeConn != nil {
		err = errors.Join(err, r.runtimeConn.Close())
	}
	return err
}

func (r *RuntimeService) Version(apiVersion string, _ ...grpc.CallOption) (*runtimeapi.VersionResponse, error) {
	return r.runtimeService.Version(context.Background(), apiVersion)
}

func (r *RuntimeService) RunPodSandbox(config *runtimeapi.PodSandboxConfig, runtimeHandler string, _ ...grpc.CallOption) (string, error) {
	return r.runtimeService.RunPodSandbox(context.Background(), config, runtimeHandler)
}

func (r *RuntimeService) UpdatePodSandboxResources(podSandboxID string, overhead *runtimeapi.LinuxContainerResources, resources *runtimeapi.LinuxContainerResources, _ ...grpc.CallOption) error {
	_, err := r.runtimeService.UpdatePodSandboxResources(context.Background(), &runtimeapi.UpdatePodSandboxResourcesRequest{
		PodSandboxId: podSandboxID,
		Overhead:     overhead,
		Resources:    resources,
	})
	return err
}

func (r *RuntimeService) StopPodSandbox(podSandboxID string, _ ...grpc.CallOption) error {
	return r.runtimeService.StopPodSandbox(context.Background(), podSandboxID)
}

func (r *RuntimeService) RemovePodSandbox(podSandboxID string, _ ...grpc.CallOption) error {
	return r.runtimeService.RemovePodSandbox(context.Background(), podSandboxID)
}

func (r *RuntimeService) PodSandboxStatus(podSandboxID string, _ ...grpc.CallOption) (*runtimeapi.PodSandboxStatus, error) {
	resp, err := r.runtimeService.PodSandboxStatus(context.Background(), podSandboxID, false)
	if err != nil {
		return nil, err
	}
	return resp.GetStatus(), nil
}

func (r *RuntimeService) ListPodSandbox(filter *runtimeapi.PodSandboxFilter, _ ...grpc.CallOption) ([]*runtimeapi.PodSandbox, error) {
	return r.runtimeService.ListPodSandbox(context.Background(), filter)
}

func (r *RuntimeService) CreateContainer(podSandboxID string, config *runtimeapi.ContainerConfig, sandboxConfig *runtimeapi.PodSandboxConfig, _ ...grpc.CallOption) (string, error) {
	return r.runtimeService.CreateContainer(context.Background(), podSandboxID, config, sandboxConfig)
}

func (r *RuntimeService) StartContainer(containerID string, _ ...grpc.CallOption) error {
	return r.runtimeService.StartContainer(context.Background(), containerID)
}

func (r *RuntimeService) StopContainer(containerID string, timeout int64, _ ...grpc.CallOption) error {
	return r.runtimeService.StopContainer(context.Background(), containerID, timeout)
}

func (r *RuntimeService) RemoveContainer(containerID string, _ ...grpc.CallOption) error {
	return r.runtimeService.RemoveContainer(context.Background(), containerID)
}

func (r *RuntimeService) ListContainers(filter *runtimeapi.ContainerFilter, _ ...grpc.CallOption) ([]*runtimeapi.Container, error) {
	return r.runtimeService.ListContainers(context.Background(), filter)
}

func (r *RuntimeService) ContainerStatus(containerID string, _ ...grpc.CallOption) (*runtimeapi.ContainerStatus, error) {
	resp, err := r.runtimeService.ContainerStatus(context.Background(), containerID, false)
	if err != nil {
		return nil, err
	}
	return resp.GetStatus(), nil
}

func (r *RuntimeService) UpdateContainerResources(containerID string, resources *runtimeapi.LinuxContainerResources, windowsResources *runtimeapi.WindowsContainerResources, _ ...grpc.CallOption) error {
	return r.runtimeService.UpdateContainerResources(context.Background(), containerID, &runtimeapi.ContainerResources{
		Linux:   resources,
		Windows: windowsResources,
	})
}

func (r *RuntimeService) ExecSync(containerID string, cmd []string, timeout time.Duration, _ ...grpc.CallOption) ([]byte, []byte, error) {
	return r.runtimeService.ExecSync(context.Background(), containerID, cmd, timeout)
}

func (r *RuntimeService) Exec(req *runtimeapi.ExecRequest, _ ...grpc.CallOption) (*runtimeapi.ExecResponse, error) {
	return r.runtimeService.Exec(context.Background(), req)
}

func (r *RuntimeService) Attach(req *runtimeapi.AttachRequest, _ ...grpc.CallOption) (*runtimeapi.AttachResponse, error) {
	return r.runtimeService.Attach(context.Background(), req)
}

func (r *RuntimeService) PortForward(req *runtimeapi.PortForwardRequest, _ ...grpc.CallOption) (*runtimeapi.PortForwardResponse, error) {
	return r.runtimeService.PortForward(context.Background(), req)
}

func (r *RuntimeService) UpdateRuntimeConfig(runtimeConfig *runtimeapi.RuntimeConfig, _ ...grpc.CallOption) error {
	return r.runtimeService.UpdateRuntimeConfig(context.Background(), runtimeConfig)
}

func (r *RuntimeService) Status(_ ...grpc.CallOption) (*runtimeapi.StatusResponse, error) {
	return r.runtimeService.Status(context.Background(), true)
}

func (r *RuntimeService) RuntimeConfig(_ *runtimeapi.RuntimeConfigRequest, _ ...grpc.CallOption) (*runtimeapi.RuntimeConfigResponse, error) {
	return r.runtimeService.RuntimeConfig(context.Background())
}

func (r *RuntimeService) ContainerStats(containerID string, _ ...grpc.CallOption) (*runtimeapi.ContainerStats, error) {
	return r.runtimeService.ContainerStats(context.Background(), containerID)
}

func (r *RuntimeService) ListContainerStats(filter *runtimeapi.ContainerStatsFilter, _ ...grpc.CallOption) ([]*runtimeapi.ContainerStats, error) {
	return r.runtimeService.ListContainerStats(context.Background(), filter)
}

func (r *RuntimeService) ReopenContainerLog(containerID string, _ ...grpc.CallOption) error {
	return r.runtimeService.ReopenContainerLog(context.Background(), containerID)
}

// GetContainerEvents uses the extra raw grpc client connection kept alongside
// the upstream runtime service. The upstream RuntimeService interface exposes
// GetContainerEvents via a channel/callback helper, but containerd's
// integration tests still assert on the stream-shaped RPC directly and need
// multiple listeners on the same CRI endpoint.
func (r *RuntimeService) GetContainerEvents(ctx context.Context, request *runtimeapi.GetEventsRequest, opts ...grpc.CallOption) (runtimeapi.RuntimeService_GetContainerEventsClient, error) {
	return r.runtimeClient.GetContainerEvents(ctx, request, opts...)
}
