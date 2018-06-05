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
	"context"

	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"k8s.io/kubernetes/pkg/kubelet/util"

	api "github.com/containerd/cri/pkg/api/v1"
)

// NewCRIPluginClient creates grpc client of cri plugin
// TODO(random-liu): Wrap grpc functions.
func NewCRIPluginClient(ctx context.Context, endpoint string) (api.CRIPluginServiceClient, error) {
	addr, dialer, err := util.GetAddressAndDialer(endpoint)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get dialer")
	}
	conn, err := grpc.DialContext(ctx, addr,
		grpc.WithBlock(),
		grpc.WithInsecure(),
		grpc.WithDialer(dialer),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to dial")
	}
	return api.NewCRIPluginServiceClient(conn), nil
}
