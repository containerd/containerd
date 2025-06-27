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

package healthcheck

import (
	"context"

	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type Service struct {
	serve *health.Server
}

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.GRPCPlugin,
		ID:   "healthcheck",
		InitFn: func(*plugin.InitContext) (interface{}, error) {
			return newService()
		},
	})
}

func newService() (*Service, error) {
	return &Service{
		health.NewServer(),
	}, nil
}

func (s *Service) Register(server *grpc.Server) error {
	grpc_health_v1.RegisterHealthServer(server, s.serve)
	return nil
}

func (s *Service) IsServing(ctx context.Context) (bool, error) {
	r, err := s.serve.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		return false, err
	}
	return r.Status == grpc_health_v1.HealthCheckResponse_SERVING, nil
}
