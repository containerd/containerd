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
	"errors"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	runtime_alpha "github.com/containerd/containerd/third_party/k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"github.com/containerd/containerd/tracing"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
)

const (
	// criSpanPrefix is a prefix for CRI server specific spans
	criSpanPrefix = "pkg.cri.server"
)

// criService is an CRI server dependency to be wrapped with instrumentation.
type criService interface {
	GRPCServices

	IsInitialized() bool

	// AlphaVersion returns the runtime name, runtime version and runtime API version.
	AlphaVersion(ctx context.Context, r *runtime_alpha.VersionRequest) (*runtime_alpha.VersionResponse, error)
}

// GRPCServices are all the grpc services provided by cri containerd.
type GRPCServices interface {
	runtime.RuntimeServiceServer
	runtime.ImageServiceServer
}

type GRPCAlphaServices interface {
	runtime_alpha.RuntimeServiceServer
	runtime_alpha.ImageServiceServer
}

// instrumentedService wraps service with containerd namespace and logs.
type instrumentedService struct {
	c criService
}

func NewService(c criService) GRPCServices {
	return &instrumentedService{c: c}
}

// instrumentedAlphaService wraps service with containerd namespace and logs.
type instrumentedAlphaService struct {
	c criService
	runtime_alpha.UnimplementedRuntimeServiceServer
	runtime_alpha.UnimplementedImageServiceServer
}

func NewAlphaService(c criService) GRPCAlphaServices {
	return &instrumentedAlphaService{c: c}
}

// checkInitialized returns error if the server is not fully initialized.
// GRPC service request handlers should return error before server is fully
// initialized.
// NOTE(random-liu): All following functions MUST check initialized at the beginning.
func (in *instrumentedService) checkInitialized() error {
	if in.c.IsInitialized() {
		return nil
	}
	return errors.New("server is not initialized yet")
}

// checkInitialized returns error if the server is not fully initialized.
// GRPC service request handlers should return error before server is fully
// initialized.
// NOTE(random-liu): All following functions MUST check initialized at the beginning.
func (in *instrumentedAlphaService) checkInitialized() error {
	if in.c.IsInitialized() {
		return nil
	}
	return errors.New("server is not initialized yet")
}

func (in *instrumentedService) RunPodSandbox(ctx context.Context, r *runtime.RunPodSandboxRequest) (res *runtime.RunPodSandboxResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Infof("RunPodSandbox for %+v", r.GetConfig().GetMetadata())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("RunPodSandbox for %+v failed, error", r.GetConfig().GetMetadata())
		} else {
			log.G(ctx).Infof("RunPodSandbox for %+v returns sandbox id %q", r.GetConfig().GetMetadata(), res.GetPodSandboxId())
		}
	}()
	res, err = in.c.RunPodSandbox(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) RunPodSandbox(ctx context.Context, r *runtime_alpha.RunPodSandboxRequest) (res *runtime_alpha.RunPodSandboxResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Infof("RunPodSandbox for %+v", r.GetConfig().GetMetadata())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("RunPodSandbox for %+v failed, error", r.GetConfig().GetMetadata())
		} else {
			log.G(ctx).Infof("RunPodSandbox for %+v returns sandbox id %q", r.GetConfig().GetMetadata(), res.GetPodSandboxId())
		}
	}()
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.RunPodSandboxRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.RunPodSandboxResponse
	v1res, err = in.c.RunPodSandbox(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.RunPodSandboxResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("RunPodSandbox for %+v failed, error", r.GetConfig().GetMetadata())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
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
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) ListPodSandbox(ctx context.Context, r *runtime_alpha.ListPodSandboxRequest) (res *runtime_alpha.ListPodSandboxResponse, err error) {
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
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.ListPodSandboxRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.ListPodSandboxResponse
	v1res, err = in.c.ListPodSandbox(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.ListPodSandboxResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Error("ListPodSandbox failed")
			}
		}
	}
	return res, errdefs.ToGRPC(err)
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
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) PodSandboxStatus(ctx context.Context, r *runtime_alpha.PodSandboxStatusRequest) (res *runtime_alpha.PodSandboxStatusResponse, err error) {
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
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.PodSandboxStatusRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.PodSandboxStatusResponse
	v1res, err = in.c.PodSandboxStatus(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.PodSandboxStatusResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("PodSandboxStatus for %q failed", r.GetPodSandboxId())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedService) StopPodSandbox(ctx context.Context, r *runtime.StopPodSandboxRequest) (_ *runtime.StopPodSandboxResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Infof("StopPodSandbox for %q", r.GetPodSandboxId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("StopPodSandbox for %q failed", r.GetPodSandboxId())
		} else {
			log.G(ctx).Infof("StopPodSandbox for %q returns successfully", r.GetPodSandboxId())
		}
	}()
	res, err := in.c.StopPodSandbox(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) StopPodSandbox(ctx context.Context, r *runtime_alpha.StopPodSandboxRequest) (res *runtime_alpha.StopPodSandboxResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Infof("StopPodSandbox for %q", r.GetPodSandboxId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("StopPodSandbox for %q failed", r.GetPodSandboxId())
		} else {
			log.G(ctx).Infof("StopPodSandbox for %q returns successfully", r.GetPodSandboxId())
		}
	}()
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.StopPodSandboxRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.StopPodSandboxResponse
	v1res, err = in.c.StopPodSandbox(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.StopPodSandboxResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("StopPodSandbox for %q failed", r.GetPodSandboxId())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedService) RemovePodSandbox(ctx context.Context, r *runtime.RemovePodSandboxRequest) (_ *runtime.RemovePodSandboxResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Infof("RemovePodSandbox for %q", r.GetPodSandboxId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("RemovePodSandbox for %q failed", r.GetPodSandboxId())
		} else {
			log.G(ctx).Infof("RemovePodSandbox %q returns successfully", r.GetPodSandboxId())
		}
	}()
	res, err := in.c.RemovePodSandbox(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) RemovePodSandbox(ctx context.Context, r *runtime_alpha.RemovePodSandboxRequest) (res *runtime_alpha.RemovePodSandboxResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Infof("RemovePodSandbox for %q", r.GetPodSandboxId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("RemovePodSandbox for %q failed", r.GetPodSandboxId())
		} else {
			log.G(ctx).Infof("RemovePodSandbox %q returns successfully", r.GetPodSandboxId())
		}
	}()
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.RemovePodSandboxRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.RemovePodSandboxResponse
	v1res, err = in.c.RemovePodSandbox(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.RemovePodSandboxResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("RemovePodSandbox for %q failed", r.GetPodSandboxId())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
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
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) PortForward(ctx context.Context, r *runtime_alpha.PortForwardRequest) (res *runtime_alpha.PortForwardResponse, err error) {
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
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.PortForwardRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.PortForwardResponse
	v1res, err = in.c.PortForward(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.PortForwardResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("Portforward for %q failed", r.GetPodSandboxId())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedService) CreateContainer(ctx context.Context, r *runtime.CreateContainerRequest) (res *runtime.CreateContainerResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
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
	}()
	res, err = in.c.CreateContainer(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) CreateContainer(ctx context.Context, r *runtime_alpha.CreateContainerRequest) (res *runtime_alpha.CreateContainerResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
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
	}()
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.CreateContainerRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.CreateContainerResponse
	v1res, err = in.c.CreateContainer(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.CreateContainerResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("CreateContainer within sandbox %q for %+v failed",
					r.GetPodSandboxId(), r.GetConfig().GetMetadata())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedService) StartContainer(ctx context.Context, r *runtime.StartContainerRequest) (_ *runtime.StartContainerResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Infof("StartContainer for %q", r.GetContainerId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("StartContainer for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Infof("StartContainer for %q returns successfully", r.GetContainerId())
		}
	}()
	res, err := in.c.StartContainer(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) StartContainer(ctx context.Context, r *runtime_alpha.StartContainerRequest) (res *runtime_alpha.StartContainerResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Infof("StartContainer for %q", r.GetContainerId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("StartContainer for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Infof("StartContainer for %q returns successfully", r.GetContainerId())
		}
	}()
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.StartContainerRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.StartContainerResponse
	v1res, err = in.c.StartContainer(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.StartContainerResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("StartContainer for %q failed", r.GetContainerId())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
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
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) ListContainers(ctx context.Context, r *runtime_alpha.ListContainersRequest) (res *runtime_alpha.ListContainersResponse, err error) {
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
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.ListContainersRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.ListContainersResponse
	v1res, err = in.c.ListContainers(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.ListContainersResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("ListContainers with filter %+v failed", r.GetFilter())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
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
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) ContainerStatus(ctx context.Context, r *runtime_alpha.ContainerStatusRequest) (res *runtime_alpha.ContainerStatusResponse, err error) {
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
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.ContainerStatusRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.ContainerStatusResponse
	v1res, err = in.c.ContainerStatus(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.ContainerStatusResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("ContainerStatus for %q failed", r.GetContainerId())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedService) StopContainer(ctx context.Context, r *runtime.StopContainerRequest) (res *runtime.StopContainerResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Infof("StopContainer for %q with timeout %d (s)", r.GetContainerId(), r.GetTimeout())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("StopContainer for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Infof("StopContainer for %q returns successfully", r.GetContainerId())
		}
	}()
	res, err = in.c.StopContainer(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) StopContainer(ctx context.Context, r *runtime_alpha.StopContainerRequest) (res *runtime_alpha.StopContainerResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Infof("StopContainer for %q with timeout %d (s)", r.GetContainerId(), r.GetTimeout())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("StopContainer for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Infof("StopContainer for %q returns successfully", r.GetContainerId())
		}
	}()
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.StopContainerRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.StopContainerResponse
	v1res, err = in.c.StopContainer(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.StopContainerResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("StopContainer for %q failed", r.GetContainerId())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedService) RemoveContainer(ctx context.Context, r *runtime.RemoveContainerRequest) (res *runtime.RemoveContainerResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Infof("RemoveContainer for %q", r.GetContainerId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("RemoveContainer for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Infof("RemoveContainer for %q returns successfully", r.GetContainerId())
		}
	}()
	res, err = in.c.RemoveContainer(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) RemoveContainer(ctx context.Context, r *runtime_alpha.RemoveContainerRequest) (res *runtime_alpha.RemoveContainerResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Infof("RemoveContainer for %q", r.GetContainerId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("RemoveContainer for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Infof("RemoveContainer for %q returns successfully", r.GetContainerId())
		}
	}()
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.RemoveContainerRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.RemoveContainerResponse
	v1res, err = in.c.RemoveContainer(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.RemoveContainerResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("RemoveContainer for %q failed", r.GetContainerId())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedService) ExecSync(ctx context.Context, r *runtime.ExecSyncRequest) (res *runtime.ExecSyncResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Debugf("ExecSync for %q with command %+v and timeout %d (s)", r.GetContainerId(), r.GetCmd(), r.GetTimeout())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ExecSync for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Debugf("ExecSync for %q returns with exit code %d", r.GetContainerId(), res.GetExitCode())
		}
	}()
	res, err = in.c.ExecSync(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) ExecSync(ctx context.Context, r *runtime_alpha.ExecSyncRequest) (res *runtime_alpha.ExecSyncResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Debugf("ExecSync for %q with command %+v and timeout %d (s)", r.GetContainerId(), r.GetCmd(), r.GetTimeout())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ExecSync for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Debugf("ExecSync for %q returns with exit code %d", r.GetContainerId(), res.GetExitCode())
		}
	}()
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.ExecSyncRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.ExecSyncResponse
	v1res, err = in.c.ExecSync(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.ExecSyncResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("ExecSync for %q failed", r.GetContainerId())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedService) Exec(ctx context.Context, r *runtime.ExecRequest) (res *runtime.ExecResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Debugf("Exec for %q with command %+v, tty %v and stdin %v",
		r.GetContainerId(), r.GetCmd(), r.GetTty(), r.GetStdin())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("Exec for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Debugf("Exec for %q returns URL %q", r.GetContainerId(), res.GetUrl())
		}
	}()
	res, err = in.c.Exec(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) Exec(ctx context.Context, r *runtime_alpha.ExecRequest) (res *runtime_alpha.ExecResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Debugf("Exec for %q with command %+v, tty %v and stdin %v",
		r.GetContainerId(), r.GetCmd(), r.GetTty(), r.GetStdin())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("Exec for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Debugf("Exec for %q returns URL %q", r.GetContainerId(), res.GetUrl())
		}
	}()
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.ExecRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.ExecResponse
	v1res, err = in.c.Exec(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.ExecResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("Exec for %q failed", r.GetContainerId())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedService) Attach(ctx context.Context, r *runtime.AttachRequest) (res *runtime.AttachResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Debugf("Attach for %q with tty %v and stdin %v", r.GetContainerId(), r.GetTty(), r.GetStdin())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("Attach for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Debugf("Attach for %q returns URL %q", r.GetContainerId(), res.Url)
		}
	}()
	res, err = in.c.Attach(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) Attach(ctx context.Context, r *runtime_alpha.AttachRequest) (res *runtime_alpha.AttachResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Debugf("Attach for %q with tty %v and stdin %v", r.GetContainerId(), r.GetTty(), r.GetStdin())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("Attach for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Debugf("Attach for %q returns URL %q", r.GetContainerId(), res.Url)
		}
	}()
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.AttachRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.AttachResponse
	v1res, err = in.c.Attach(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.AttachResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("Attach for %q failed", r.GetContainerId())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
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
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) UpdateContainerResources(ctx context.Context, r *runtime_alpha.UpdateContainerResourcesRequest) (res *runtime_alpha.UpdateContainerResourcesResponse, err error) {
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
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.UpdateContainerResourcesRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.UpdateContainerResourcesResponse
	v1res, err = in.c.UpdateContainerResources(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.UpdateContainerResourcesResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("UpdateContainerResources for %q failed", r.GetContainerId())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedService) PullImage(ctx context.Context, r *runtime.PullImageRequest) (res *runtime.PullImageResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	ctx, span := tracing.StartSpan(ctx, tracing.Name(criSpanPrefix, "PullImage"))
	defer span.End()
	log.G(ctx).Infof("PullImage %q", r.GetImage().GetImage())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("PullImage %q failed", r.GetImage().GetImage())
		} else {
			log.G(ctx).Infof("PullImage %q returns image reference %q",
				r.GetImage().GetImage(), res.GetImageRef())
		}
		span.SetStatus(err)
	}()
	res, err = in.c.PullImage(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) PullImage(ctx context.Context, r *runtime_alpha.PullImageRequest) (res *runtime_alpha.PullImageResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	ctx, span := tracing.StartSpan(ctx, tracing.Name(criSpanPrefix, "PullImage"))
	defer span.End()
	log.G(ctx).Infof("PullImage %q", r.GetImage().GetImage())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("PullImage %q failed", r.GetImage().GetImage())
		} else {
			log.G(ctx).Infof("PullImage %q returns image reference %q",
				r.GetImage().GetImage(), res.GetImageRef())
		}
		span.SetStatus(err)
	}()
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.PullImageRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.PullImageResponse
	v1res, err = in.c.PullImage(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.PullImageResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("PullImage %q failed", r.GetImage().GetImage())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedService) ListImages(ctx context.Context, r *runtime.ListImagesRequest) (res *runtime.ListImagesResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	ctx, span := tracing.StartSpan(ctx, tracing.Name(criSpanPrefix, "ListImages"))
	defer span.End()
	log.G(ctx).Tracef("ListImages with filter %+v", r.GetFilter())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ListImages with filter %+v failed", r.GetFilter())
		} else {
			log.G(ctx).Tracef("ListImages with filter %+v returns image list %+v",
				r.GetFilter(), res.GetImages())
		}
		span.SetStatus(err)
	}()
	res, err = in.c.ListImages(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) ListImages(ctx context.Context, r *runtime_alpha.ListImagesRequest) (res *runtime_alpha.ListImagesResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	ctx, span := tracing.StartSpan(ctx, tracing.Name(criSpanPrefix, "ListImages"))
	defer span.End()
	log.G(ctx).Tracef("ListImages with filter %+v", r.GetFilter())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ListImages with filter %+v failed", r.GetFilter())
		} else {
			log.G(ctx).Tracef("ListImages with filter %+v returns image list %+v",
				r.GetFilter(), res.GetImages())
		}
		span.SetStatus(err)
	}()
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.ListImagesRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.ListImagesResponse
	v1res, err = in.c.ListImages(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.ListImagesResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("ListImages with filter %+v failed", r.GetFilter())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedService) ImageStatus(ctx context.Context, r *runtime.ImageStatusRequest) (res *runtime.ImageStatusResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	ctx, span := tracing.StartSpan(ctx, tracing.Name(criSpanPrefix, "ImageStatus"))
	defer span.End()
	log.G(ctx).Tracef("ImageStatus for %q", r.GetImage().GetImage())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ImageStatus for %q failed", r.GetImage().GetImage())
		} else {
			log.G(ctx).Tracef("ImageStatus for %q returns image status %+v",
				r.GetImage().GetImage(), res.GetImage())
		}
		span.SetStatus(err)
	}()
	res, err = in.c.ImageStatus(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) ImageStatus(ctx context.Context, r *runtime_alpha.ImageStatusRequest) (res *runtime_alpha.ImageStatusResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	ctx, span := tracing.StartSpan(ctx, tracing.Name(criSpanPrefix, "ImageStatus"))
	defer span.End()
	log.G(ctx).Tracef("ImageStatus for %q", r.GetImage().GetImage())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ImageStatus for %q failed", r.GetImage().GetImage())
		} else {
			log.G(ctx).Tracef("ImageStatus for %q returns image status %+v",
				r.GetImage().GetImage(), res.GetImage())
		}
		span.SetStatus(err)
	}()
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.ImageStatusRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.ImageStatusResponse
	v1res, err = in.c.ImageStatus(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.ImageStatusResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("ImageStatus for %q failed", r.GetImage().GetImage())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedService) RemoveImage(ctx context.Context, r *runtime.RemoveImageRequest) (_ *runtime.RemoveImageResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	ctx, span := tracing.StartSpan(ctx, tracing.Name(criSpanPrefix, "RemoveImage"))
	defer span.End()
	log.G(ctx).Infof("RemoveImage %q", r.GetImage().GetImage())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("RemoveImage %q failed", r.GetImage().GetImage())
		} else {
			log.G(ctx).Infof("RemoveImage %q returns successfully", r.GetImage().GetImage())
		}
		span.SetStatus(err)
	}()
	res, err := in.c.RemoveImage(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) RemoveImage(ctx context.Context, r *runtime_alpha.RemoveImageRequest) (res *runtime_alpha.RemoveImageResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	ctx, span := tracing.StartSpan(ctx, tracing.Name(criSpanPrefix, "RemoveImage"))
	defer span.End()
	log.G(ctx).Infof("RemoveImage %q", r.GetImage().GetImage())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("RemoveImage %q failed", r.GetImage().GetImage())
		} else {
			log.G(ctx).Infof("RemoveImage %q returns successfully", r.GetImage().GetImage())
		}
		span.SetStatus(err)
	}()
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.RemoveImageRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.RemoveImageResponse
	v1res, err = in.c.RemoveImage(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.RemoveImageResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("RemoveImage %q failed", r.GetImage().GetImage())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedService) ImageFsInfo(ctx context.Context, r *runtime.ImageFsInfoRequest) (res *runtime.ImageFsInfoResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	ctx, span := tracing.StartSpan(ctx, tracing.Name(criSpanPrefix, "ImageFsInfo"))
	defer span.End()
	log.G(ctx).Debugf("ImageFsInfo")
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Error("ImageFsInfo failed")
		} else {
			log.G(ctx).Debugf("ImageFsInfo returns filesystem info %+v", res.ImageFilesystems)
		}
		span.SetStatus(err)
	}()
	res, err = in.c.ImageFsInfo(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) ImageFsInfo(ctx context.Context, r *runtime_alpha.ImageFsInfoRequest) (res *runtime_alpha.ImageFsInfoResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	ctx, span := tracing.StartSpan(ctx, tracing.Name(criSpanPrefix, "ImageFsInfo"))
	defer span.End()
	log.G(ctx).Debugf("ImageFsInfo")
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Error("ImageFsInfo failed")
		} else {
			log.G(ctx).Debugf("ImageFsInfo returns filesystem info %+v", res.ImageFilesystems)
		}
		span.SetStatus(err)
	}()
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.ImageFsInfoRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.ImageFsInfoResponse
	v1res, err = in.c.ImageFsInfo(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.ImageFsInfoResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Error("ImageFsInfo failed")
			}
		}
	}
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedService) PodSandboxStats(ctx context.Context, r *runtime.PodSandboxStatsRequest) (res *runtime.PodSandboxStatsResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Debugf("PodSandboxStats for %q", r.GetPodSandboxId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("PodSandboxStats for %q failed", r.GetPodSandboxId())
		} else {
			log.G(ctx).Debugf("PodSandboxStats for %q returns stats %+v", r.GetPodSandboxId(), res.GetStats())
		}
	}()
	res, err = in.c.PodSandboxStats(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) PodSandboxStats(ctx context.Context, r *runtime_alpha.PodSandboxStatsRequest) (res *runtime_alpha.PodSandboxStatsResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Debugf("PodSandboxStats for %q", r.GetPodSandboxId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("PodSandboxStats for %q failed", r.GetPodSandboxId())
		} else {
			log.G(ctx).Debugf("PodSandboxStats for %q returns stats %+v", r.GetPodSandboxId(), res.GetStats())
		}
	}()
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.PodSandboxStatsRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.PodSandboxStatsResponse
	v1res, err = in.c.PodSandboxStats(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.PodSandboxStatsResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(err).Errorf("PodSandboxStats for %q failed", r.GetPodSandboxId())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedService) ContainerStats(ctx context.Context, r *runtime.ContainerStatsRequest) (res *runtime.ContainerStatsResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Debugf("ContainerStats for %q", r.GetContainerId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ContainerStats for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Debugf("ContainerStats for %q returns stats %+v", r.GetContainerId(), res.GetStats())
		}
	}()
	res, err = in.c.ContainerStats(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) ContainerStats(ctx context.Context, r *runtime_alpha.ContainerStatsRequest) (res *runtime_alpha.ContainerStatsResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}
	log.G(ctx).Debugf("ContainerStats for %q", r.GetContainerId())
	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ContainerStats for %q failed", r.GetContainerId())
		} else {
			log.G(ctx).Debugf("ContainerStats for %q returns stats %+v", r.GetContainerId(), res.GetStats())
		}
	}()
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.ContainerStatsRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.ContainerStatsResponse
	v1res, err = in.c.ContainerStats(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.ContainerStatsResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("ContainerStats for %q failed", r.GetContainerId())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
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
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) ListPodSandboxStats(ctx context.Context, r *runtime_alpha.ListPodSandboxStatsRequest) (res *runtime_alpha.ListPodSandboxStatsResponse, err error) {
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
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.ListPodSandboxStatsRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.ListPodSandboxStatsResponse
	v1res, err = in.c.ListPodSandboxStats(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.ListPodSandboxStatsResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Error("ListPodSandboxStats failed")
			}
		}
	}
	return res, errdefs.ToGRPC(err)
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
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) ListContainerStats(ctx context.Context, r *runtime_alpha.ListContainerStatsRequest) (res *runtime_alpha.ListContainerStatsResponse, err error) {
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
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.ListContainerStatsRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.ListContainerStatsResponse
	v1res, err = in.c.ListContainerStats(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.ListContainerStatsResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Error("ListContainerStats failed")
			}
		}
	}
	return res, errdefs.ToGRPC(err)
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
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) Status(ctx context.Context, r *runtime_alpha.StatusRequest) (res *runtime_alpha.StatusResponse, err error) {
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
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.StatusRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.StatusResponse
	v1res, err = in.c.Status(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.StatusResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Error("Status failed")
			}
		}
	}
	return res, errdefs.ToGRPC(err)
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
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) Version(ctx context.Context, r *runtime_alpha.VersionRequest) (res *runtime_alpha.VersionResponse, err error) {
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
	res, err = in.c.AlphaVersion(ctrdutil.WithNamespace(ctx), r)
	return res, errdefs.ToGRPC(err)
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
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) UpdateRuntimeConfig(ctx context.Context, r *runtime_alpha.UpdateRuntimeConfigRequest) (res *runtime_alpha.UpdateRuntimeConfigResponse, err error) {
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
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.UpdateRuntimeConfigRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.UpdateRuntimeConfigResponse
	v1res, err = in.c.UpdateRuntimeConfig(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.UpdateRuntimeConfigResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Error("UpdateRuntimeConfig failed")
			}
		}
	}
	return res, errdefs.ToGRPC(err)
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
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedAlphaService) ReopenContainerLog(ctx context.Context, r *runtime_alpha.ReopenContainerLogRequest) (res *runtime_alpha.ReopenContainerLogResponse, err error) {
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
	// converts request and response for earlier CRI version to call and get response from the current version
	var v1r runtime.ReopenContainerLogRequest
	if err := ctrdutil.AlphaReqToV1Req(r, &v1r); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	var v1res *runtime.ReopenContainerLogResponse
	v1res, err = in.c.ReopenContainerLog(ctrdutil.WithNamespace(ctx), &v1r)
	if v1res != nil {
		resp := &runtime_alpha.ReopenContainerLogResponse{}
		perr := ctrdutil.V1RespToAlphaResp(v1res, resp)
		if perr == nil {
			res = resp
		} else {
			// actual error has precidence on error returned vs parse error issues
			if err == nil {
				err = perr
			} else {
				// extra log entry if convert response parse error and request error
				log.G(ctx).WithError(perr).Errorf("ReopenContainerLog for %q failed", r.GetContainerId())
			}
		}
	}
	return res, errdefs.ToGRPC(err)
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

	res, err = in.c.CheckpointContainer(ctx, r)
	return res, errdefs.ToGRPC(err)
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
	return errdefs.ToGRPC(err)
}

func (in *instrumentedService) ListMetricDescriptors(ctx context.Context, r *runtime.ListMetricDescriptorsRequest) (res *runtime.ListMetricDescriptorsResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ListMetricDescriptors failed, error")
		} else {
			log.G(ctx).Debug("ListMetricDescriptors returns successfully")
		}
	}()

	res, err = in.c.ListMetricDescriptors(ctx, r)
	return res, errdefs.ToGRPC(err)
}

func (in *instrumentedService) ListPodSandboxMetrics(ctx context.Context, r *runtime.ListPodSandboxMetricsRequest) (res *runtime.ListPodSandboxMetricsResponse, err error) {
	if err := in.checkInitialized(); err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			log.G(ctx).WithError(err).Errorf("ListPodSandboxMetrics failed, error")
		} else {
			log.G(ctx).Debug("ListPodSandboxMetrics returns successfully")
		}
	}()

	res, err = in.c.ListPodSandboxMetrics(ctx, r)
	return res, errdefs.ToGRPC(err)
}
