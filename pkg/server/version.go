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
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	"github.com/kubernetes-incubator/cri-containerd/pkg/version"
)

const (
	// For now, containerd and runc are bundled with cri-containerd, cri-containerd
	// version is more important to us.
	// TODO(random-liu): Figure out how to package cri-containerd and containerd,
	// and how to version it. We still prefer calling the container runtime "containerd",
	// but we care both the cri-containerd version and containerd version.
	containerName        = "cri-containerd"
	containerdAPIVersion = "0.0.0"
	// kubeAPIVersion is the api version of kubernetes.
	kubeAPIVersion = "0.1.0"
)

// Version returns the runtime name, runtime version and runtime API version.
func (c *criContainerdService) Version(ctx context.Context, r *runtime.VersionRequest) (*runtime.VersionResponse, error) {
	return &runtime.VersionResponse{
		Version:        kubeAPIVersion,
		RuntimeName:    containerName,
		RuntimeVersion: version.CRIContainerdVersion,
		// Containerd doesn't have an api version now.
		RuntimeApiVersion: containerdAPIVersion,
	}, nil
}
