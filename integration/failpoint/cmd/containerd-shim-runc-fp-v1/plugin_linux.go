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

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	taskapi "github.com/containerd/containerd/api/runtime/task/v2"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/pkg/failpoint"
	"github.com/containerd/containerd/pkg/shutdown"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/plugin/registry"
	"github.com/containerd/containerd/plugins"
	"github.com/containerd/containerd/runtime/v2/runc/task"
	"github.com/containerd/containerd/runtime/v2/shim"
	"github.com/containerd/ttrpc"
)

const (
	failpointPrefixKey = "io.containerd.runtime.v2.shim.failpoint."
)

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.TTRPCPlugin,
		ID:   "task",
		Requires: []plugin.Type{
			plugins.EventPlugin,
			plugins.InternalPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			pp, err := ic.GetByID(plugins.EventPlugin, "publisher")
			if err != nil {
				return nil, err
			}
			ss, err := ic.GetByID(plugins.InternalPlugin, "shutdown")
			if err != nil {
				return nil, err
			}
			fps, err := newFailpointFromOCIAnnotation()
			if err != nil {
				return nil, err
			}
			service, err := task.NewTaskService(ic.Context, pp.(shim.Publisher), ss.(shutdown.Service))
			if err != nil {
				return nil, err
			}

			return &taskServiceWithFp{
				fps:   fps,
				local: service,
			}, nil
		},
	})
}

var (
	_ = shim.TTRPCServerOptioner(&taskServiceWithFp{})
)

type taskServiceWithFp struct {
	fps   map[string]*failpoint.Failpoint
	local taskapi.TaskService
}

func (s *taskServiceWithFp) RegisterTTRPC(server *ttrpc.Server) error {
	taskapi.RegisterTaskService(server, s.local)
	return nil
}

func (s *taskServiceWithFp) UnaryInterceptor() ttrpc.UnaryServerInterceptor {
	return func(ctx context.Context, unmarshal ttrpc.Unmarshaler, info *ttrpc.UnaryServerInfo, method ttrpc.Method) (interface{}, error) {
		methodName := filepath.Base(info.FullMethod)
		if fp, ok := s.fps[methodName]; ok {
			if err := fp.Evaluate(); err != nil {
				return nil, err
			}
		}
		return method(ctx, unmarshal)
	}
}

// newFailpointFromOCIAnnotation reloads and parses the annotation from
// bundle-path/config.json.
//
// The annotation controlling task API's failpoint should be like:
//
//	io.containerd.runtime.v2.shim.failpoint.Create = 1*off->1*error(please retry)
//
// The `Create` is the shim unary API and the value of annotation is the
// failpoint control. The function will return a set of failpoint controllers.
func newFailpointFromOCIAnnotation() (map[string]*failpoint.Failpoint, error) {
	// NOTE: shim's current working dir is in bundle dir.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current working dir: %w", err)
	}

	configPath := filepath.Join(cwd, oci.ConfigFilename)
	spec, err := oci.ReadSpec(configPath)
	if err != nil {
		return nil, err
	}

	res := make(map[string]*failpoint.Failpoint)
	for k, v := range spec.Annotations {
		if !strings.HasPrefix(k, failpointPrefixKey) {
			continue
		}

		methodName := strings.TrimPrefix(k, failpointPrefixKey)
		fp, err := failpoint.NewFailpoint(methodName, v)
		if err != nil {
			return nil, fmt.Errorf("failed to parse failpoint %v: %w", v, err)
		}
		res[methodName] = fp
	}
	return res, nil
}
