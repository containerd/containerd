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

package instrument

import (
	"context"

	"github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errgrpc"
	"github.com/containerd/log"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/pkg/tracing"

	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
)

// criService is an CRI server dependency to be wrapped with instrumentation.
type criService interface {
	GRPCServices

	IsInitialized() bool
}

// GRPCServices are all the grpc services provided by cri containerd.
type GRPCServices interface {
	runtime.RuntimeServiceServer
	runtime.ImageServiceServer
}

// instrumentedService wraps service with containerd namespace and logs.
type instrumentedService struct {
	runtime.UnimplementedRuntimeServiceServer
	runtime.UnimplementedImageServiceServer

	c criService
}

func NewService(c criService) GRPCServices {
	return &instrumentedService{c: c}
}

// checkInitialized returns error if the server is not fully initialized.
// GRPC service request handlers should return error before server is fully
// initialized.
// NOTE(random-liu): All following functions MUST check initialized at the beginning.
func (in *instrumentedService) checkInitialized() error {
	if in.c.IsInitialized() {
		return nil
	}
	return errgrpc.ToGRPCf(errdefs.ErrUnavailable, "server is not initialized yet")
}

func (in *instrumentedService) RunPodSandbox(ctx context.Context, r *runtime.RunPodSandboxRequest) (res *runtime.RunPodSandboxResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	span := tracing.SpanFromContext(ctx)
	log.G(ctx).Infof("RunPodSandbox for %+v", r.GetConfig().GetMetadata())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("RunPodSandbox for %+v failed, error", r.GetConfig().GetMetadata())
		} else {
			log.G(ctx).Infof("RunPodSandbox for %+v returns sandbox id %q", r.GetConfig().GetMetadata(), res.GetPodSandboxId())
		}
		span.RecordError(err)
	}()
	res, err = in.c.RunPodSandbox(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) ListPodSandbox(ctx context.Context, r *runtime.ListPodSandboxRequest) (res *runtime.ListPodSandboxResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Tracef("ListPodSandbox with filter %+v", r.GetFilter())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Error("ListPodSandbox failed")
		} else {
			log.G(ctx).Tracef("ListPodSandbox returns pod sandboxes %+v", res.GetItems())
		}
	}()
	res, err = in.c.ListPodSandbox(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) PodSandboxStatus(ctx context.Context, r *runtime.PodSandboxStatusRequest) (res *runtime.PodSandboxStatusResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Tracef("PodSandboxStatus for %q", r.GetPodSandboxId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("PodSandboxStatus for %q failed", r.GetPodSandboxId())
		} else {
			log.G(ctx).Tracef("PodSandboxStatus for %q returns status %+v", r.GetPodSandboxId(), res.GetStatus())
		}
	}()
	res, err = in.c.PodSandboxStatus(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) StopPodSandbox(ctx context.Context, r *runtime.StopPodSandboxRequest) (_ *runtime.StopPodSandboxResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	span := tracing.SpanFromContext(ctx)
	log.G(ctx).Infof("StopPodSandbox for %q", r.GetPodSandboxId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("StopPodSandbox for %q failed", r.GetPodSandboxId())
		} else {
			log.G(ctx).Infof("StopPodSandbox for %q returns successfully", r.GetPodSandboxId())
		}
		span.RecordError(err)
	}()
	res, err := in.c.StopPodSandbox(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) RemovePodSandbox(ctx context.Context, r *runtime.RemovePodSandboxRequest) (_ *runtime.RemovePodSandboxResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	span := tracing.SpanFromContext(ctx)
	log.G(ctx).Infof("RemovePodSandbox for %q", r.GetPodSandboxId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("RemovePodSandbox for %q failed", r.GetPodSandboxId())
		} else {
			log.G(ctx).Infof("RemovePodSandbox %q returns successfully", r.GetPodSandboxId())
		}
		span.RecordError(err)
	}()
	res, err := in.c.RemovePodSandbox(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) PortForward(ctx context.Context, r *runtime.PortForwardRequest) (res *runtime.PortForwardResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Infof("Portforward for %q port %v", r.GetPodSandboxId(), r.GetPort())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("Portforward for %q failed", r.GetPodSandboxId())
		} else {
			log.G(ctx).Infof("Portforward for %q returns URL %q", r.GetPodSandboxId(), res.GetUrl())
		}
	}()
	res, err = in.c.PortForward(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) CreateContainer(ctx context.Context, r *runtime.CreateContainerRequest) (res *runtime.CreateContainerResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	span := tracing.SpanFromContext(ctx)
	log.G(ctx).Infof("CreateContainer within sandbox %q for container %+v",
		r.GetPodSandboxId(), r.GetConfig().GetMetadata())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("CreateContainer within sandbox %q for %+v failed",
				r.GetPodSandboxId(), r.GetConfig().GetMetadata())
		} else {
			log.G(ctx).Infof("CreateContainer within sandbox %q for %+v returns container id %q",
				r.GetPodSandboxId(), r.GetConfig().GetMetadata(), res.GetContainerId())
		}
		span.RecordError(err)
	}()
	res, err = in.c.CreateContainer(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) StartContainer(ctx context.Context, r *runtime.StartContainerRequest) (_ *runtime.StartContainerResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	span := tracing.SpanFromContext(ctx)
	log.G(ctx).Infof("StartContainer for %q", r.GetContainerId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("StartContainer for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Infof("StartContainer for %q returns successfully", r.GetContainerId())
		}
		span.RecordError(err)
	}()
	res, err := in.c.StartContainer(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) ListContainers(ctx context.Context, r *runtime.ListContainersRequest) (res *runtime.ListContainersResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Tracef("ListContainers with filter %+v", r.GetFilter())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ListContainers with filter %+v failed", r.GetFilter())
		} else {
			log.G(ctx).Tracef("ListContainers with filter %+v returns containers %+v",
				r.GetFilter(), res.GetContainers())
		}
	}()
	res, err = in.c.ListContainers(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) ContainerStatus(ctx context.Context, r *runtime.ContainerStatusRequest) (res *runtime.ContainerStatusResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Tracef("ContainerStatus for %q", r.GetContainerId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ContainerStatus for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Tracef("ContainerStatus for %q returns status %+v", r.GetContainerId(), res.GetStatus())
		}
	}()
	res, err = in.c.ContainerStatus(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) StopContainer(ctx context.Context, r *runtime.StopContainerRequest) (res *runtime.StopContainerResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	span := tracing.SpanFromContext(ctx)
	log.G(ctx).Infof("StopContainer for %q with timeout %d (s)", r.GetContainerId(), r.GetTimeout())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("StopContainer for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Infof("StopContainer for %q returns successfully", r.GetContainerId())
		}
		span.RecordError(err)
	}()
	res, err = in.c.StopContainer(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) RemoveContainer(ctx context.Context, r *runtime.RemoveContainerRequest) (res *runtime.RemoveContainerResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	span := tracing.SpanFromContext(ctx)
	log.G(ctx).Infof("RemoveContainer for %q", r.GetContainerId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("RemoveContainer for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Infof("RemoveContainer for %q returns successfully", r.GetContainerId())
		}
		span.RecordError(err)
	}()
	res, err = in.c.RemoveContainer(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) ExecSync(ctx context.Context, r *runtime.ExecSyncRequest) (res *runtime.ExecSyncResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	span := tracing.SpanFromContext(ctx)
	log.G(ctx).Debugf("ExecSync for %q with command %+v and timeout %d (s)", r.GetContainerId(), r.GetCmd(), r.GetTimeout())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ExecSync for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Tracef("ExecSync for %q returns with exit code %d", r.GetContainerId(), res.GetExitCode())
		}
		span.RecordError(err)
	}()
	res, err = in.c.ExecSync(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) Exec(ctx context.Context, r *runtime.ExecRequest) (res *runtime.ExecResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	span := tracing.SpanFromContext(ctx)
	log.G(ctx).Debugf("Exec for %q with command %+v, tty %v and stdin %v",
		r.GetContainerId(), r.GetCmd(), r.GetTty(), r.GetStdin())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("Exec for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Debugf("Exec for %q returns URL %q", r.GetContainerId(), res.GetUrl())
		}
		span.RecordError(err)
	}()
	res, err = in.c.Exec(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) Attach(ctx context.Context, r *runtime.AttachRequest) (res *runtime.AttachResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	span := tracing.SpanFromContext(ctx)
	log.G(ctx).Debugf("Attach for %q with tty %v and stdin %v", r.GetContainerId(), r.GetTty(), r.GetStdin())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("Attach for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Debugf("Attach for %q returns URL %q", r.GetContainerId(), res.Url)
		}
		span.RecordError(err)
	}()
	res, err = in.c.Attach(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) UpdateContainerResources(ctx context.Context, r *runtime.UpdateContainerResourcesRequest) (res *runtime.UpdateContainerResourcesResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Infof("UpdateContainerResources for %q with Linux: %+v / Windows: %+v", r.GetContainerId(), r.GetLinux(), r.GetWindows())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("UpdateContainerResources for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Infof("UpdateContainerResources for %q returns successfully", r.GetContainerId())
		}
	}()
	res, err = in.c.UpdateContainerResources(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) PullImage(ctx context.Context, r *runtime.PullImageRequest) (res *runtime.PullImageResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	span := tracing.SpanFromContext(ctx)
	log.G(ctx).Infof("PullImage %q", r.GetImage().GetImage())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("PullImage %q failed", r.GetImage().GetImage())
		} else {
			log.G(ctx).Infof("PullImage %q returns image reference %q",
				r.GetImage().GetImage(), res.GetImageRef())
		}
		span.RecordError(err)
	}()
	res, err = in.c.PullImage(ctrdutil.WithNamespace(ctx), r)
	// Sanitize error to remove sensitive information from both logs and returned gRPC error
	if err != nil {
		err = ctrdutil.SanitizeError(err)
	}
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) ListImages(ctx context.Context, r *runtime.ListImagesRequest) (res *runtime.ListImagesResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Tracef("ListImages with filter %+v", r.GetFilter())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ListImages with filter %+v failed", r.GetFilter())
		} else {
			log.G(ctx).Tracef("ListImages with filter %+v returns image list %+v",
				r.GetFilter(), res.GetImages())
		}
	}()
	res, err = in.c.ListImages(ctrdutil.WithNamespace(ctx), r)
	// Sanitize error to remove sensitive information from both logs and returned gRPC error
	if err != nil {
		err = ctrdutil.SanitizeError(err)
	}
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) ImageStatus(ctx context.Context, r *runtime.ImageStatusRequest) (res *runtime.ImageStatusResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Tracef("ImageStatus for %q", r.GetImage().GetImage())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ImageStatus for %q failed", r.GetImage().GetImage())
		} else {
			log.G(ctx).Tracef("ImageStatus for %q returns image status %+v",
				r.GetImage().GetImage(), res.GetImage())
		}
	}()
	res, err = in.c.ImageStatus(ctrdutil.WithNamespace(ctx), r)
	// Sanitize error to remove sensitive information from both logs and returned gRPC error
	if err != nil {
		err = ctrdutil.SanitizeError(err)
	}
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) RemoveImage(ctx context.Context, r *runtime.RemoveImageRequest) (_ *runtime.RemoveImageResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	span := tracing.SpanFromContext(ctx)
	log.G(ctx).Infof("RemoveImage %q", r.GetImage().GetImage())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("RemoveImage %q failed", r.GetImage().GetImage())
		} else {
			log.G(ctx).Infof("RemoveImage %q returns successfully", r.GetImage().GetImage())
		}
		span.RecordError(err)
	}()
	res, err := in.c.RemoveImage(ctrdutil.WithNamespace(ctx), r)
	// Sanitize error to remove sensitive information from both logs and returned gRPC error
	if err != nil {
		err = ctrdutil.SanitizeError(err)
	}
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) ImageFsInfo(ctx context.Context, r *runtime.ImageFsInfoRequest) (res *runtime.ImageFsInfoResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Tracef("ImageFsInfo")
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Error("ImageFsInfo failed")
		} else {
			log.G(ctx).Tracef("ImageFsInfo returns filesystem info %+v", res.ImageFilesystems)
		}
	}()
	res, err = in.c.ImageFsInfo(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) PodSandboxStats(ctx context.Context, r *runtime.PodSandboxStatsRequest) (res *runtime.PodSandboxStatsResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Tracef("PodSandboxStats for %q", r.GetPodSandboxId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("PodSandboxStats for %q failed", r.GetPodSandboxId())
		} else {
			log.G(ctx).Tracef("PodSandboxStats for %q returns stats %+v", r.GetPodSandboxId(), res.GetStats())
		}
	}()
	res, err = in.c.PodSandboxStats(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) ContainerStats(ctx context.Context, r *runtime.ContainerStatsRequest) (res *runtime.ContainerStatsResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Tracef("ContainerStats for %q", r.GetContainerId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ContainerStats for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Tracef("ContainerStats for %q returns stats %+v", r.GetContainerId(), res.GetStats())
		}
	}()
	res, err = in.c.ContainerStats(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) ListPodSandboxStats(ctx context.Context, r *runtime.ListPodSandboxStatsRequest) (res *runtime.ListPodSandboxStatsResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Tracef("ListPodSandboxStats with filter %+v", r.GetFilter())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Error("ListPodSandboxStats failed")
		} else {
			log.G(ctx).Tracef("ListPodSandboxStats returns stats %+v", res.GetStats())
		}
	}()
	res, err = in.c.ListPodSandboxStats(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) ListContainerStats(ctx context.Context, r *runtime.ListContainerStatsRequest) (res *runtime.ListContainerStatsResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Tracef("ListContainerStats with filter %+v", r.GetFilter())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Error("ListContainerStats failed")
		} else {
			log.G(ctx).Tracef("ListContainerStats returns stats %+v", res.GetStats())
		}
	}()
	res, err = in.c.ListContainerStats(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) Status(ctx context.Context, r *runtime.StatusRequest) (res *runtime.StatusResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Tracef("Status")
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Error("Status failed")
		} else {
			log.G(ctx).Tracef("Status returns status %+v", res.GetStatus())
		}
	}()
	res, err = in.c.Status(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) Version(ctx context.Context, r *runtime.VersionRequest) (res *runtime.VersionResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Tracef("Version with client side version %q", r.GetVersion())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Error("Version failed")
		} else {
			log.G(ctx).Tracef("Version returns %+v", res)
		}
	}()
	res, err = in.c.Version(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) UpdateRuntimeConfig(ctx context.Context, r *runtime.UpdateRuntimeConfigRequest) (res *runtime.UpdateRuntimeConfigResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Debugf("UpdateRuntimeConfig with config %+v", r.GetRuntimeConfig())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Error("UpdateRuntimeConfig failed")
		} else {
			log.G(ctx).Debug("UpdateRuntimeConfig returns successfully")
		}
	}()
	res, err = in.c.UpdateRuntimeConfig(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) ReopenContainerLog(ctx context.Context, r *runtime.ReopenContainerLogRequest) (res *runtime.ReopenContainerLogResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Debugf("ReopenContainerLog for %q", r.GetContainerId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ReopenContainerLog for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Debugf("ReopenContainerLog for %q returns successfully", r.GetContainerId())
		}
	}()
	res, err = in.c.ReopenContainerLog(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) CheckpointContainer(ctx context.Context, r *runtime.CheckpointContainerRequest) (res *runtime.CheckpointContainerResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("CheckpointContainer failed, error")
		} else {
			log.G(ctx).Debug("CheckpointContainer returns successfully")
		}
	}()

	res, err = in.c.CheckpointContainer(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) GetContainerEvents(r *runtime.GetEventsRequest, s runtime.RuntimeService_GetContainerEventsServer) (err error) {
	if err := in.checkInitialized(); err != nil {
		return err
	}

	ctx := s.Context()
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("GetContainerEvents failed, error")
		} else {
			log.G(ctx).Debug("GetContainerEvents returns successfully")
		}
	}()

	err = in.c.GetContainerEvents(r, s)
	return errgrpc.ToGRPC(err)
}

func (in *instrumentedService) ListMetricDescriptors(ctx context.Context, r *runtime.ListMetricDescriptorsRequest) (res *runtime.ListMetricDescriptorsResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ListMetricDescriptors failed, error")
		} else {
			log.G(ctx).Trace("ListMetricDescriptors returns successfully")
		}
	}()

	res, err = in.c.ListMetricDescriptors(ctx, r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) ListPodSandboxMetrics(ctx context.Context, r *runtime.ListPodSandboxMetricsRequest) (res *runtime.ListPodSandboxMetricsResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ListPodSandboxMetrics failed, error")
		} else {
			log.G(ctx).Trace("ListPodSandboxMetrics returns successfully")
		}
	}()

	res, err = in.c.ListPodSandboxMetrics(ctx, r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) RuntimeConfig(ctx context.Context, r *runtime.RuntimeConfigRequest) (res *runtime.RuntimeConfigResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Tracef("RuntimeConfig")
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Error("RuntimeConfig failed")
		} else {
			log.G(ctx).Tracef("RuntimeConfig returns config %+v", res)
		}
	}()
	res, err = in.c.RuntimeConfig(ctx, r)
	return res, errgrpc.ToGRPC(err)
}

func (in *instrumentedService) UpdatePodSandboxResources(ctx context.Context, r *runtime.UpdatePodSandboxResourcesRequest) (res *runtime.UpdatePodSandboxResourcesResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Infof("UpdatePodSandboxResources for %q with Overhead: %+v / Resources: %+v", r.GetPodSandboxId(), r.GetOverhead(), r.GetResources())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("UpdatePodSandboxResources for %q failed", r.GetPodSandboxId())
		} else {
			log.G(ctx).Infof("UpdatePodSandboxResources for %q returns successfully", r.GetPodSandboxId())
		}
	}()
	res, err = in.c.UpdatePodSandboxResources(ctrdutil.WithNamespace(ctx), r)
	return res, errgrpc.ToGRPC(err)
}
