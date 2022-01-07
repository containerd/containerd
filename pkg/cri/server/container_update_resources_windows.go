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
	"fmt"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/net/context"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	criconfig "github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/pkg/cri/opts"
	"github.com/containerd/containerd/pkg/cri/util"
)

// updateOCIResource updates container resource limit.
func updateOCIResource(ctx context.Context, spec *runtimespec.Spec, r *runtime.UpdateContainerResourcesRequest,
	config criconfig.Config) (*runtimespec.Spec, error) {

	// Copy to make sure old spec is not changed.
	var cloned runtimespec.Spec
	if err := util.DeepCopy(&cloned, spec); err != nil {
		return nil, fmt.Errorf("failed to deep copy: %w", err)
	}
	if cloned.Windows == nil {
		cloned.Windows = &runtimespec.Windows{}
	}
	if err := opts.WithWindowsResources(r.GetWindows())(ctx, nil, nil, &cloned); err != nil {
		return nil, fmt.Errorf("unable to set windows container resources: %w", err)
	}
	return &cloned, nil
}

func getResources(spec *runtimespec.Spec) interface{} {
	return spec.Windows.Resources
}
