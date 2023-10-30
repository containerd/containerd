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

package version

import (
	"context"

	api "github.com/containerd/containerd/v2/api/services/version/v1"
	"github.com/containerd/containerd/v2/plugin"
	"github.com/containerd/containerd/v2/plugin/registry"
	"github.com/containerd/containerd/v2/plugins"
	ptypes "github.com/containerd/containerd/v2/protobuf/types"
	ctrdversion "github.com/containerd/containerd/v2/version"
	"google.golang.org/grpc"
)

var _ api.VersionServer = &service{}

func init() {
	registry.Register(&plugin.Registration{
		Type:   plugins.GRPCPlugin,
		ID:     "version",
		InitFn: initFunc,
	})
}

func initFunc(ic *plugin.InitContext) (interface{}, error) {
	return &service{}, nil
}

type service struct {
	api.UnimplementedVersionServer
}

func (s *service) Register(server *grpc.Server) error {
	api.RegisterVersionServer(server, s)
	return nil
}

func (s *service) Version(ctx context.Context, _ *ptypes.Empty) (*api.VersionResponse, error) {
	return &api.VersionResponse{
		Version:  ctrdversion.Version,
		Revision: ctrdversion.Revision,
	}, nil
}
