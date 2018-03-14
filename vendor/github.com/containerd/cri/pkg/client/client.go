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

package client

import (
	"fmt"
	"time"

	"google.golang.org/grpc"
	"k8s.io/kubernetes/pkg/kubelet/util"

	api "github.com/containerd/cri/pkg/api/v1"
)

// NewCRIContainerdClient creates grpc client of cri-containerd
// TODO(random-liu): Wrap grpc functions.
func NewCRIContainerdClient(endpoint string, timeout time.Duration) (api.CRIContainerdServiceClient, error) {
	addr, dialer, err := util.GetAddressAndDialer(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to get dialer: %v", err)
	}
	conn, err := grpc.Dial(addr,
		grpc.WithBlock(),
		grpc.WithInsecure(),
		// TODO(random-liu): WithTimeout is being deprecated, use context instead.
		grpc.WithTimeout(timeout),
		grpc.WithDialer(dialer),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %v", err)
	}
	return api.NewCRIContainerdServiceClient(conn), nil
}
