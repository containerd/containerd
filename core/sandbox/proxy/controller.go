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

package proxy

import (
	"context"
	"time"

	api "github.com/containerd/containerd/api/services/sandbox/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/containerd/v2/pkg/protobuf"
	"github.com/containerd/errdefs"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"google.golang.org/protobuf/types/known/anypb"
)

// remoteSandboxController is a low level GRPC client for containerd's sandbox controller service
type remoteSandboxController struct {
	client api.ControllerClient
}

var _ sandbox.Controller = (*remoteSandboxController)(nil)

// NewSandboxController creates a client for a sandbox controller
func NewSandboxController(client api.ControllerClient) sandbox.Controller {
	return &remoteSandboxController{client: client}
}

func (s *remoteSandboxController) Create(ctx context.Context, sandboxInfo sandbox.Sandbox, opts ...sandbox.CreateOpt) error {
	var options sandbox.CreateOptions
	for _, opt := range opts {
		opt(&options)
	}
	apiSandbox, err := toAPISandbox(sandboxInfo)
	if err != nil {
		return err
	}
	_, err = s.client.Create(ctx, &api.ControllerCreateRequest{
		SandboxID: sandboxInfo.ID,
		Rootfs:    mount.ToProto(options.Rootfs),
		Options: &anypb.Any{
			TypeUrl: options.Options.GetTypeUrl(),
			Value:   options.Options.GetValue(),
		},
		NetnsPath:   options.NetNSPath,
		Annotations: options.Annotations,
		Sandbox:     &apiSandbox,
	})
	if err != nil {
		return errdefs.FromGRPC(err)
	}

	return nil
}

func (s *remoteSandboxController) Start(ctx context.Context, sandboxID string) (sandbox.ControllerInstance, error) {
	resp, err := s.client.Start(ctx, &api.ControllerStartRequest{SandboxID: sandboxID})
	if err != nil {
		return sandbox.ControllerInstance{}, errdefs.FromGRPC(err)
	}

	return sandbox.ControllerInstance{
		SandboxID: sandboxID,
		Pid:       resp.GetPid(),
		CreatedAt: resp.GetCreatedAt().AsTime(),
		Labels:    resp.GetLabels(),
		Address:   resp.GetAddress(),
		Version:   resp.GetVersion(),
	}, nil
}

func (s *remoteSandboxController) Platform(ctx context.Context, sandboxID string) (imagespec.Platform, error) {
	resp, err := s.client.Platform(ctx, &api.ControllerPlatformRequest{SandboxID: sandboxID})
	if err != nil {
		return imagespec.Platform{}, errdefs.FromGRPC(err)
	}

	platform := resp.GetPlatform()
	return imagespec.Platform{
		Architecture: platform.GetArchitecture(),
		OS:           platform.GetOS(),
		Variant:      platform.GetVariant(),
	}, nil
}

func (s *remoteSandboxController) Stop(ctx context.Context, sandboxID string, opts ...sandbox.StopOpt) error {
	var soptions sandbox.StopOptions
	for _, opt := range opts {
		opt(&soptions)
	}
	req := &api.ControllerStopRequest{SandboxID: sandboxID}
	if soptions.Timeout != nil {
		req.TimeoutSecs = uint32(soptions.Timeout.Seconds())
	}
	_, err := s.client.Stop(ctx, req)
	if err != nil {
		return errdefs.FromGRPC(err)
	}

	return nil
}

func (s *remoteSandboxController) Shutdown(ctx context.Context, sandboxID string) error {
	_, err := s.client.Shutdown(ctx, &api.ControllerShutdownRequest{SandboxID: sandboxID})
	if err != nil {
		return errdefs.FromGRPC(err)
	}

	return nil
}

func (s *remoteSandboxController) Wait(ctx context.Context, sandboxID string) (sandbox.ExitStatus, error) {
	// For remote sandbox controllers, the controller process may restart,
	// we have to retry if the error indicates that it is the grpc disconnection.
	var (
		resp          *api.ControllerWaitResponse
		err           error
		retryInterval time.Duration = 128
	)
	for {
		resp, err = s.client.Wait(ctx, &api.ControllerWaitRequest{SandboxID: sandboxID})
		if err != nil {
			grpcErr := errdefs.FromGRPC(err)
			if !errdefs.IsUnavailable(grpcErr) {
				return sandbox.ExitStatus{}, grpcErr
			}
			select {
			case <-time.After(retryInterval * time.Millisecond):
				if retryInterval < 4096 {
					retryInterval = retryInterval << 1
				}
				continue
			case <-ctx.Done():
				return sandbox.ExitStatus{}, grpcErr
			}
		}
		break
	}

	return sandbox.ExitStatus{
		ExitStatus: resp.GetExitStatus(),
		ExitedAt:   resp.GetExitedAt().AsTime(),
	}, nil
}

func (s *remoteSandboxController) Status(ctx context.Context, sandboxID string, verbose bool) (sandbox.ControllerStatus, error) {
	resp, err := s.client.Status(ctx, &api.ControllerStatusRequest{SandboxID: sandboxID, Verbose: verbose})
	if err != nil {
		return sandbox.ControllerStatus{}, errdefs.FromGRPC(err)
	}
	return sandbox.ControllerStatus{
		SandboxID: sandboxID,
		Pid:       resp.GetPid(),
		State:     resp.GetState(),
		Info:      resp.GetInfo(),
		CreatedAt: resp.GetCreatedAt().AsTime(),
		ExitedAt:  resp.GetExitedAt().AsTime(),
		Extra:     resp.GetExtra(),
		Address:   resp.GetAddress(),
		Version:   resp.GetVersion(),
	}, nil
}

func (s *remoteSandboxController) Metrics(ctx context.Context, sandboxID string) (*types.Metric, error) {
	resp, err := s.client.Metrics(ctx, &api.ControllerMetricsRequest{SandboxID: sandboxID})
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}
	return resp.Metrics, nil
}

func (s *remoteSandboxController) Update(
	ctx context.Context,
	sandboxID string,
	sandbox sandbox.Sandbox,
	fields ...string) error {
	apiSandbox, err := toAPISandbox(sandbox)
	if err != nil {
		return err
	}
	_, err = s.client.Update(ctx, &api.ControllerUpdateRequest{
		SandboxID: sandboxID,
		Sandbox:   &apiSandbox,
		Fields:    fields,
	})
	if err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

func toAPISandbox(sb sandbox.Sandbox) (types.Sandbox, error) {
	options, err := protobuf.MarshalAnyToProto(sb.Runtime.Options)
	if err != nil {
		return types.Sandbox{}, err
	}
	spec, err := protobuf.MarshalAnyToProto(sb.Spec)
	if err != nil {
		return types.Sandbox{}, err
	}
	extensions := make(map[string]*anypb.Any)
	for k, v := range sb.Extensions {
		pb, err := protobuf.MarshalAnyToProto(v)
		if err != nil {
			return types.Sandbox{}, err
		}
		extensions[k] = pb
	}
	return types.Sandbox{
		SandboxID: sb.ID,
		Runtime: &types.Sandbox_Runtime{
			Name:    sb.Runtime.Name,
			Options: options,
		},
		Spec:       spec,
		Labels:     sb.Labels,
		CreatedAt:  protobuf.ToTimestamp(sb.CreatedAt),
		UpdatedAt:  protobuf.ToTimestamp(sb.UpdatedAt),
		Extensions: extensions,
		Sandboxer:  sb.Sandboxer,
	}, nil
}
