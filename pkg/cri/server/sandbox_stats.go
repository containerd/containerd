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
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func (c *criService) PodSandboxStats(
	ctx context.Context,
	r *runtime.PodSandboxStatsRequest,
) (*runtime.PodSandboxStatsResponse, error) {

	sandbox, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		return nil, errors.Wrapf(err, "an error occurred when trying to find sandbox %s", r.GetPodSandboxId())
	}

	metrics, err := metricsForSandbox(sandbox)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting metrics for sandbox %s", r.GetPodSandboxId())
	}

	podSandboxStats, err := c.podSandboxStats(ctx, sandbox, metrics)

	if err != nil {
		return nil, errors.Wrapf(err, "failed to decode pod sandbox metrics %s", r.GetPodSandboxId())
	}

	return &runtime.PodSandboxStatsResponse{Stats: podSandboxStats}, nil
}
