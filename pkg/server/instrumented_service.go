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
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	api "github.com/containerd/cri-containerd/pkg/api/v1"
	"github.com/containerd/cri-containerd/pkg/log"
)

// instrumentedService wraps service and logs each operation.
type instrumentedService struct {
	*criContainerdService
}

func newInstrumentedService(c *criContainerdService) CRIContainerdService {
	return &instrumentedService{criContainerdService: c}
}

func (in *instrumentedService) RunPodSandbox(ctx context.Context, r *runtime.RunPodSandboxRequest) (res *runtime.RunPodSandboxResponse, err error) {
	logrus.Infof("RunPodSandbox with config %+v", r.GetConfig())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("RunPodSandbox for %+v failed, error", r.GetConfig().GetMetadata())
		} else {
			logrus.Infof("RunPodSandbox for %+v returns sandbox id %q", r.GetConfig().GetMetadata(), res.GetPodSandboxId())
		}
	}()
	return in.criContainerdService.RunPodSandbox(ctx, r)
}

func (in *instrumentedService) ListPodSandbox(ctx context.Context, r *runtime.ListPodSandboxRequest) (res *runtime.ListPodSandboxResponse, err error) {
	log.Tracef("ListPodSandbox with filter %+v", r.GetFilter())
	defer func() {
		if err != nil {
			logrus.WithError(err).Error("ListPodSandbox failed")
		} else {
			log.Tracef("ListPodSandbox returns sandboxes %+v", res.GetItems())
		}
	}()
	return in.criContainerdService.ListPodSandbox(ctx, r)
}

func (in *instrumentedService) PodSandboxStatus(ctx context.Context, r *runtime.PodSandboxStatusRequest) (res *runtime.PodSandboxStatusResponse, err error) {
	log.Tracef("PodSandboxStatus for %q", r.GetPodSandboxId())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("PodSandboxStatus for %q failed", r.GetPodSandboxId())
		} else {
			log.Tracef("PodSandboxStatus for %q returns status %+v", r.GetPodSandboxId(), res.GetStatus())
		}
	}()
	return in.criContainerdService.PodSandboxStatus(ctx, r)
}

func (in *instrumentedService) StopPodSandbox(ctx context.Context, r *runtime.StopPodSandboxRequest) (_ *runtime.StopPodSandboxResponse, err error) {
	logrus.Infof("StopPodSandbox for %q", r.GetPodSandboxId())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("StopPodSandbox for %q failed", r.GetPodSandboxId())
		} else {
			logrus.Infof("StopPodSandbox for %q returns successfully", r.GetPodSandboxId())
		}
	}()
	return in.criContainerdService.StopPodSandbox(ctx, r)
}

func (in *instrumentedService) RemovePodSandbox(ctx context.Context, r *runtime.RemovePodSandboxRequest) (_ *runtime.RemovePodSandboxResponse, err error) {
	logrus.Infof("RemovePodSandbox for %q", r.GetPodSandboxId())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("RemovePodSandbox for %q failed", r.GetPodSandboxId())
		} else {
			logrus.Infof("RemovePodSandbox %q returns successfully", r.GetPodSandboxId())
		}
	}()
	return in.criContainerdService.RemovePodSandbox(ctx, r)
}

func (in *instrumentedService) PortForward(ctx context.Context, r *runtime.PortForwardRequest) (res *runtime.PortForwardResponse, err error) {
	logrus.Infof("Portforward for %q port %v", r.GetPodSandboxId(), r.GetPort())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("Portforward for %q failed", r.GetPodSandboxId())
		} else {
			logrus.Infof("Portforward for %q returns URL %q", r.GetPodSandboxId(), res.GetUrl())
		}
	}()
	return in.criContainerdService.PortForward(ctx, r)
}

func (in *instrumentedService) CreateContainer(ctx context.Context, r *runtime.CreateContainerRequest) (res *runtime.CreateContainerResponse, err error) {
	logrus.Infof("CreateContainer within sandbox %q with container config %+v and sandbox config %+v",
		r.GetPodSandboxId(), r.GetConfig(), r.GetSandboxConfig())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("CreateContainer within sandbox %q for %+v failed",
				r.GetPodSandboxId(), r.GetConfig().GetMetadata())
		} else {
			logrus.Infof("CreateContainer within sandbox %q for %+v returns container id %q",
				r.GetPodSandboxId(), r.GetConfig().GetMetadata(), res.GetContainerId())
		}
	}()
	return in.criContainerdService.CreateContainer(ctx, r)
}

func (in *instrumentedService) StartContainer(ctx context.Context, r *runtime.StartContainerRequest) (_ *runtime.StartContainerResponse, err error) {
	logrus.Infof("StartContainer for %q", r.GetContainerId())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("StartContainer for %q failed", r.GetContainerId())
		} else {
			logrus.Infof("StartContainer for %q returns successfully", r.GetContainerId())
		}
	}()
	return in.criContainerdService.StartContainer(ctx, r)
}

func (in *instrumentedService) ListContainers(ctx context.Context, r *runtime.ListContainersRequest) (res *runtime.ListContainersResponse, err error) {
	log.Tracef("ListContainers with filter %+v", r.GetFilter())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("ListContainers with filter %+v failed", r.GetFilter())
		} else {
			log.Tracef("ListContainers with filter %+v returns containers %+v",
				r.GetFilter(), res.GetContainers())
		}
	}()
	return in.criContainerdService.ListContainers(ctx, r)
}

func (in *instrumentedService) ContainerStatus(ctx context.Context, r *runtime.ContainerStatusRequest) (res *runtime.ContainerStatusResponse, err error) {
	log.Tracef("ContainerStatus for %q", r.GetContainerId())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("ContainerStatus for %q failed", r.GetContainerId())
		} else {
			log.Tracef("ContainerStatus for %q returns status %+v", r.GetContainerId(), res.GetStatus())
		}
	}()
	return in.criContainerdService.ContainerStatus(ctx, r)
}

func (in *instrumentedService) StopContainer(ctx context.Context, r *runtime.StopContainerRequest) (res *runtime.StopContainerResponse, err error) {
	logrus.Infof("StopContainer for %q with timeout %d (s)", r.GetContainerId(), r.GetTimeout())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("StopContainer for %q failed", r.GetContainerId())
		} else {
			logrus.Infof("StopContainer for %q returns successfully", r.GetContainerId())
		}
	}()
	return in.criContainerdService.StopContainer(ctx, r)
}

func (in *instrumentedService) RemoveContainer(ctx context.Context, r *runtime.RemoveContainerRequest) (res *runtime.RemoveContainerResponse, err error) {
	logrus.Infof("RemoveContainer for %q", r.GetContainerId())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("RemoveContainer for %q failed", r.GetContainerId())
		} else {
			logrus.Infof("RemoveContainer for %q returns successfully", r.GetContainerId())
		}
	}()
	return in.criContainerdService.RemoveContainer(ctx, r)
}

func (in *instrumentedService) ExecSync(ctx context.Context, r *runtime.ExecSyncRequest) (res *runtime.ExecSyncResponse, err error) {
	logrus.Infof("ExecSync for %q with command %+v and timeout %d (s)", r.GetContainerId(), r.GetCmd(), r.GetTimeout())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("ExecSync for %q failed", r.GetContainerId())
		} else {
			logrus.Infof("ExecSync for %q returns with exit code %d", r.GetContainerId(), res.GetExitCode())
			logrus.Debugf("ExecSync for %q outputs - stdout: %q, stderr: %q", r.GetContainerId(),
				res.GetStdout(), res.GetStderr())
		}
	}()
	return in.criContainerdService.ExecSync(ctx, r)
}

func (in *instrumentedService) Exec(ctx context.Context, r *runtime.ExecRequest) (res *runtime.ExecResponse, err error) {
	logrus.Infof("Exec for %q with command %+v, tty %v and stdin %v",
		r.GetContainerId(), r.GetCmd(), r.GetTty(), r.GetStdin())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("Exec for %q failed", r.GetContainerId())
		} else {
			logrus.Infof("Exec for %q returns URL %q", r.GetContainerId(), res.GetUrl())
		}
	}()
	return in.criContainerdService.Exec(ctx, r)
}

func (in *instrumentedService) Attach(ctx context.Context, r *runtime.AttachRequest) (res *runtime.AttachResponse, err error) {
	logrus.Infof("Attach for %q with tty %v and stdin %v", r.GetContainerId(), r.GetTty(), r.GetStdin())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("Attach for %q failed", r.GetContainerId())
		} else {
			logrus.Infof("Attach for %q returns URL %q", r.GetContainerId(), res.Url)
		}
	}()
	return in.criContainerdService.Attach(ctx, r)
}

func (in *instrumentedService) UpdateContainerResources(ctx context.Context, r *runtime.UpdateContainerResourcesRequest) (res *runtime.UpdateContainerResourcesResponse, err error) {
	logrus.Infof("UpdateContainerResources for %q with %+v", r.GetContainerId(), r.GetLinux())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("UpdateContainerResources for %q failed", r.GetContainerId())
		} else {
			logrus.Infof("UpdateContainerResources for %q returns successfully", r.GetContainerId())
		}
	}()
	return in.criContainerdService.UpdateContainerResources(ctx, r)
}

func (in *instrumentedService) PullImage(ctx context.Context, r *runtime.PullImageRequest) (res *runtime.PullImageResponse, err error) {
	logrus.Infof("PullImage %q with auth config %+v", r.GetImage().GetImage(), r.GetAuth())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("PullImage %q failed", r.GetImage().GetImage())
		} else {
			logrus.Infof("PullImage %q returns image reference %q",
				r.GetImage().GetImage(), res.GetImageRef())
		}
	}()
	return in.criContainerdService.PullImage(ctx, r)
}

func (in *instrumentedService) ListImages(ctx context.Context, r *runtime.ListImagesRequest) (res *runtime.ListImagesResponse, err error) {
	log.Tracef("ListImages with filter %+v", r.GetFilter())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("ListImages with filter %+v failed", r.GetFilter())
		} else {
			log.Tracef("ListImages with filter %+v returns image list %+v",
				r.GetFilter(), res.GetImages())
		}
	}()
	return in.criContainerdService.ListImages(ctx, r)
}

func (in *instrumentedService) ImageStatus(ctx context.Context, r *runtime.ImageStatusRequest) (res *runtime.ImageStatusResponse, err error) {
	log.Tracef("ImageStatus for %q", r.GetImage().GetImage())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("ImageStatus for %q failed", r.GetImage().GetImage())
		} else {
			log.Tracef("ImageStatus for %q returns image status %+v",
				r.GetImage().GetImage(), res.GetImage())
		}
	}()
	return in.criContainerdService.ImageStatus(ctx, r)
}

func (in *instrumentedService) RemoveImage(ctx context.Context, r *runtime.RemoveImageRequest) (_ *runtime.RemoveImageResponse, err error) {
	logrus.Infof("RemoveImage %q", r.GetImage().GetImage())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("RemoveImage %q failed", r.GetImage().GetImage())
		} else {
			logrus.Infof("RemoveImage %q returns successfully", r.GetImage().GetImage())
		}
	}()
	return in.criContainerdService.RemoveImage(ctx, r)
}

func (in *instrumentedService) ImageFsInfo(ctx context.Context, r *runtime.ImageFsInfoRequest) (res *runtime.ImageFsInfoResponse, err error) {
	logrus.Debugf("ImageFsInfo")
	defer func() {
		if err != nil {
			logrus.WithError(err).Error("ImageFsInfo failed")
		} else {
			logrus.Debugf("ImageFsInfo returns filesystem info %+v", res.ImageFilesystems)
		}
	}()
	return in.criContainerdService.ImageFsInfo(ctx, r)
}

func (in *instrumentedService) ContainerStats(ctx context.Context, r *runtime.ContainerStatsRequest) (res *runtime.ContainerStatsResponse, err error) {
	logrus.Debugf("ContainerStats for %q", r.GetContainerId())
	defer func() {
		if err != nil {
			logrus.WithError(err).Errorf("ContainerStats for %q failed", r.GetContainerId())
		} else {
			logrus.Debugf("ContainerStats for %q returns stats %+v", r.GetContainerId(), res.GetStats())
		}
	}()
	return in.criContainerdService.ContainerStats(ctx, r)
}

func (in *instrumentedService) ListContainerStats(ctx context.Context, r *runtime.ListContainerStatsRequest) (res *runtime.ListContainerStatsResponse, err error) {
	log.Tracef("ListContainerStats with filter %+v", r.GetFilter())
	defer func() {
		if err != nil {
			logrus.WithError(err).Error("ListContainerStats failed")
		} else {
			log.Tracef("ListContainerStats returns stats %+v", res.GetStats())
		}
	}()
	return in.criContainerdService.ListContainerStats(ctx, r)
}

func (in *instrumentedService) LoadImage(ctx context.Context, r *api.LoadImageRequest) (res *api.LoadImageResponse, err error) {
	logrus.Debugf("LoadImage from file %q", r.GetFilePath())
	defer func() {
		if err != nil {
			logrus.WithError(err).Error("LoadImage failed")
		} else {
			logrus.Debugf("LoadImage returns images %+v", res.GetImages())
		}
	}()
	return in.criContainerdService.LoadImage(ctx, r)
}
