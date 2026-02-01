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

	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// ContainerStatus inspects the container and returns the status.
func (c *criService) ContainerStatus(ctx context.Context, r *runtime.ContainerStatusRequest) (*runtime.ContainerStatusResponse, error) {
	container, err := c.containerStore.Get(r.GetContainerId())
	if err != nil {
		return nil, fmt.Errorf("an error occurred when try to find container %q: %w", r.GetContainerId(), err)
	}

	// TODO(random-liu): Clean up the following logic in CRI.
	// Current assumption:
	// * ImageSpec in container config is image ID.
	// * ImageSpec in container status is image tag.
	// * ImageRef in container status is repo digest (manifest list digest for multi-arch images).
	// * ImageId in container status is the node-local image config digest.
	spec := container.Config.GetImage()

	// Note: container.ImageRef holds the platform-specific image config digest that was
	// resolved during container creation. For multi-arch images, this differs from the
	// manifest list digest. We capture it here as imageID before imageRef gets overwritten
	// below with repoDigests[0] (the manifest list digest).
	imageRef := container.ImageRef
	imageID := container.ImageRef

	image, err := c.GetImage(imageRef)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get image %q: %w", imageRef, err)
		}
	} else {
		repoTags, repoDigests := util.ParseImageReferences(image.References)
		if len(repoTags) > 0 {
			// Based on current behavior of dockershim, this field should be
			// image tag.
			spec = &runtime.ImageSpec{Image: repoTags[0]}
		}
		if len(repoDigests) > 0 {
			// repoDigests[0] is the manifest list digest for multi-arch images.
			// This overwrites imageRef (originally the platform-specific digest)
			// for backwards compatibility with existing CRI consumers.
			imageRef = repoDigests[0]
		}
	}
	status, err := toCRIContainerStatus(ctx, container, spec, imageRef, imageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get ContainerStatus: %w", err)
	}
	if status.GetCreatedAt() == 0 {
		// CRI doesn't allow CreatedAt == 0.
		info, err := container.Container.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get CreatedAt in %q state: %w", status.State, err)
		}
		status.CreatedAt = info.CreatedAt.UnixNano()
	}

	info, err := toCRIContainerInfo(ctx, container, r.GetVerbose())
	if err != nil {
		return nil, fmt.Errorf("failed to get verbose container info: %w", err)
	}

	return &runtime.ContainerStatusResponse{
		Status: status,
		Info:   info,
	}, nil
}

// toCRIContainerStatus converts internal container object to CRI container status.
func toCRIContainerStatus(ctx context.Context, container containerstore.Container, spec *runtime.ImageSpec, imageRef string, imageID string) (*runtime.ContainerStatus, error) {
	meta := container.Metadata
	status := container.Status.Get()
	reason := status.Reason
	if status.State() == runtime.ContainerState_CONTAINER_EXITED && reason == "" {
		if status.ExitCode == 0 {
			reason = completeExitReason
		} else {
			reason = errorExitReason
		}
	}

	// If container is in the created state, not set started and finished unix timestamps
	var st, ft int64
	switch status.State() {
	case runtime.ContainerState_CONTAINER_RUNNING:
		// If container is in the running state, set started unix timestamps
		st = status.StartedAt
	case runtime.ContainerState_CONTAINER_EXITED, runtime.ContainerState_CONTAINER_UNKNOWN:
		st, ft = status.StartedAt, status.FinishedAt
	}

	runtimeUser, err := toCRIContainerUser(ctx, container)
	if err != nil {
		log.G(ctx).WithField("Id", meta.ID).WithError(err).Debug("failed to get ContainerUser. returning an empty ContainerUser")
		runtimeUser = &runtime.ContainerUser{}
	}

	var statusStopSignal runtime.Signal
	stopsignal := container.Config.GetStopSignal()

	if stopsignal == runtime.Signal_RUNTIME_DEFAULT {
		if container.Metadata.StopSignal != "" {
			statusStopSignal = toCRISignal(container.Metadata.StopSignal)
		} else {
			statusStopSignal = runtime.Signal_SIGTERM
		}
	} else {
		statusStopSignal = stopsignal
	}

	return &runtime.ContainerStatus{
		Id:          meta.ID,
		Metadata:    meta.Config.GetMetadata(),
		State:       status.State(),
		CreatedAt:   status.CreatedAt,
		StartedAt:   st,
		FinishedAt:  ft,
		ExitCode:    status.ExitCode,
		Image:       spec,
		ImageRef:    imageRef,
		ImageId:     imageID,
		Reason:      reason,
		Message:     status.Message,
		Labels:      meta.Config.GetLabels(),
		Annotations: meta.Config.GetAnnotations(),
		Mounts:      meta.Config.GetMounts(),
		LogPath:     meta.LogPath,
		Resources:   status.Resources,
		User:        runtimeUser,
		StopSignal:  statusStopSignal,
	}, nil
}

// ContainerInfo is extra information for a container.
type ContainerInfo struct {
	// TODO(random-liu): Add sandboxID in CRI container status.
	SandboxID      string                   `json:"sandboxID"`
	Pid            uint32                   `json:"pid"`
	Removing       bool                     `json:"removing"`
	SnapshotKey    string                   `json:"snapshotKey"`
	Snapshotter    string                   `json:"snapshotter"`
	RuntimeType    string                   `json:"runtimeType"`
	RuntimeOptions interface{}              `json:"runtimeOptions"`
	Config         *runtime.ContainerConfig `json:"config"`
	RuntimeSpec    *runtimespec.Spec        `json:"runtimeSpec"`
}

// toCRIContainerInfo converts internal container object information to CRI container status response info map.
func toCRIContainerInfo(ctx context.Context, container containerstore.Container, verbose bool) (map[string]string, error) {
	if !verbose {
		return nil, nil
	}

	meta := container.Metadata
	status := container.Status.Get()

	// TODO(random-liu): Change CRI status info to use array instead of map.
	ci := &ContainerInfo{
		SandboxID: container.SandboxID,
		Pid:       status.Pid,
		Removing:  status.Removing,
		Config:    meta.Config,
	}

	var err error
	ci.RuntimeSpec, err = container.Container.Spec(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container runtime spec: %w", err)
	}

	ctrInfo, err := container.Container.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container info: %w", err)
	}
	ci.SnapshotKey = ctrInfo.SnapshotKey
	ci.Snapshotter = ctrInfo.Snapshotter

	runtimeOptions, err := getRuntimeOptions(ctrInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to get runtime options: %w", err)
	}
	ci.RuntimeType = ctrInfo.Runtime.Name
	ci.RuntimeOptions = runtimeOptions

	infoBytes, err := json.Marshal(ci)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal info %v: %w", ci, err)
	}
	return map[string]string{
		"info": string(infoBytes),
	}, nil
}

func toCRISignal(stopsignal string) runtime.Signal {
	signalValue := runtime.Signal_RUNTIME_DEFAULT

	switch stopsignal {
	case "SIGABRT":
		signalValue = runtime.Signal_SIGABRT
	case "SIGALRM":
		signalValue = runtime.Signal_SIGALRM
	case "SIGBUS":
		signalValue = runtime.Signal_SIGBUS
	case "SIGCHLD":
		signalValue = runtime.Signal_SIGCHLD
	case "SIGCLD":
		signalValue = runtime.Signal_SIGCLD
	case "SIGCONT":
		signalValue = runtime.Signal_SIGCONT
	case "SIGFPE":
		signalValue = runtime.Signal_SIGFPE
	case "SIGHUP":
		signalValue = runtime.Signal_SIGHUP
	case "SIGILL":
		signalValue = runtime.Signal_SIGILL
	case "SIGINT":
		signalValue = runtime.Signal_SIGINT
	case "SIGIO":
		signalValue = runtime.Signal_SIGIO
	case "SIGIOT":
		signalValue = runtime.Signal_SIGIOT
	case "SIGKILL":
		signalValue = runtime.Signal_SIGKILL
	case "SIGPIPE":
		signalValue = runtime.Signal_SIGPIPE
	case "SIGPOLL":
		signalValue = runtime.Signal_SIGPOLL
	case "SIGPROF":
		signalValue = runtime.Signal_SIGPROF
	case "SIGPWR":
		signalValue = runtime.Signal_SIGPWR
	case "SIGQUIT":
		signalValue = runtime.Signal_SIGQUIT
	case "SIGSEGV":
		signalValue = runtime.Signal_SIGSEGV
	case "SIGSTKFLT":
		signalValue = runtime.Signal_SIGSTKFLT
	case "SIGSTOP":
		signalValue = runtime.Signal_SIGSTOP
	case "SIGSYS":
		signalValue = runtime.Signal_SIGSYS
	case "SIGTERM":
		signalValue = runtime.Signal_SIGTERM
	case "SIGTRAP":
		signalValue = runtime.Signal_SIGTRAP
	case "SIGTSTP":
		signalValue = runtime.Signal_SIGTSTP
	case "SIGTTIN":
		signalValue = runtime.Signal_SIGTTIN
	case "SIGTTOU":
		signalValue = runtime.Signal_SIGTTOU
	case "SIGURG":
		signalValue = runtime.Signal_SIGURG
	case "SIGUSR1":
		signalValue = runtime.Signal_SIGUSR1
	case "SIGUSR2":
		signalValue = runtime.Signal_SIGUSR2
	case "SIGVTALRM":
		signalValue = runtime.Signal_SIGVTALRM
	case "SIGWINCH":
		signalValue = runtime.Signal_SIGWINCH
	case "SIGXCPU":
		signalValue = runtime.Signal_SIGXCPU
	case "SIGXFSZ":
		signalValue = runtime.Signal_SIGXFSZ
	case "SIGRTMIN":
		signalValue = runtime.Signal_SIGRTMIN
	case "SIGRTMIN+1":
		signalValue = runtime.Signal_SIGRTMINPLUS1
	case "SIGRTMIN+2":
		signalValue = runtime.Signal_SIGRTMINPLUS2
	case "SIGRTMIN+3":
		signalValue = runtime.Signal_SIGRTMINPLUS3
	case "SIGRTMIN+4":
		signalValue = runtime.Signal_SIGRTMINPLUS4
	case "SIGRTMIN+5":
		signalValue = runtime.Signal_SIGRTMINPLUS5
	case "SIGRTMIN+6":
		signalValue = runtime.Signal_SIGRTMINPLUS6
	case "SIGRTMIN+7":
		signalValue = runtime.Signal_SIGRTMINPLUS7
	case "SIGRTMIN+8":
		signalValue = runtime.Signal_SIGRTMINPLUS8
	case "SIGRTMIN+9":
		signalValue = runtime.Signal_SIGRTMINPLUS9
	case "SIGRTMIN+10":
		signalValue = runtime.Signal_SIGRTMINPLUS10
	case "SIGRTMIN+11":
		signalValue = runtime.Signal_SIGRTMINPLUS11
	case "SIGRTMIN+12":
		signalValue = runtime.Signal_SIGRTMINPLUS12
	case "SIGRTMIN+13":
		signalValue = runtime.Signal_SIGRTMINPLUS13
	case "SIGRTMIN+14":
		signalValue = runtime.Signal_SIGRTMINPLUS14
	case "SIGRTMIN+15":
		signalValue = runtime.Signal_SIGRTMINPLUS15
	case "SIGRTMAX-14":
		signalValue = runtime.Signal_SIGRTMAXMINUS14
	case "SIGRTMAX-13":
		signalValue = runtime.Signal_SIGRTMAXMINUS13
	case "SIGRTMAX-12":
		signalValue = runtime.Signal_SIGRTMAXMINUS12
	case "SIGRTMAX-11":
		signalValue = runtime.Signal_SIGRTMAXMINUS11
	case "SIGRTMAX-10":
		signalValue = runtime.Signal_SIGRTMAXMINUS10
	case "SIGRTMAX-9":
		signalValue = runtime.Signal_SIGRTMAXMINUS9
	case "SIGRTMAX-8":
		signalValue = runtime.Signal_SIGRTMAXMINUS8
	case "SIGRTMAX-7":
		signalValue = runtime.Signal_SIGRTMAXMINUS7
	case "SIGRTMAX-6":
		signalValue = runtime.Signal_SIGRTMAXMINUS6
	case "SIGRTMAX-5":
		signalValue = runtime.Signal_SIGRTMAXMINUS5
	case "SIGRTMAX-4":
		signalValue = runtime.Signal_SIGRTMAXMINUS4
	case "SIGRTMAX-3":
		signalValue = runtime.Signal_SIGRTMAXMINUS3
	case "SIGRTMAX-2":
		signalValue = runtime.Signal_SIGRTMAXMINUS2
	case "SIGRTMAX-1":
		signalValue = runtime.Signal_SIGRTMAXMINUS1
	case "SIGRTMAX":
		signalValue = runtime.Signal_SIGRTMAX
	}

	return signalValue
}
