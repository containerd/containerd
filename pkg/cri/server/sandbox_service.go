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

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/client"
	criconfig "github.com/containerd/containerd/v2/pkg/cri/config"
	"github.com/containerd/containerd/v2/sandbox"
)

type criSandboxService struct {
	cli    *client.Client
	config *criconfig.Config
}

func newCriSandboxService(config *criconfig.Config, c *client.Client) *criSandboxService {
	return &criSandboxService{
		cli:    c,
		config: config,
	}
}

func (c *criSandboxService) SandboxController(config *runtime.PodSandboxConfig, runtimeHandler string) (sandbox.Controller, error) {
	ociRuntime, err := c.config.GetSandboxRuntime(config, runtimeHandler)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox runtime: %w", err)
	}
	return c.cli.SandboxController(ociRuntime.Sandboxer), nil
}
