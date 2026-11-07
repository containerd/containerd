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

	bootapi "github.com/containerd/containerd/api/runtime/bootstrap/v1"
	"github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/manager"
	"github.com/containerd/containerd/v2/internal/failpoint"
	"github.com/containerd/containerd/v2/pkg/shim"
)

const (
	// beforeStartShim is evaluated by the bootstrap command before it starts
	// the shim server.
	beforeStartShim = "BeforeStartShim"

	// afterStartShim is evaluated by the bootstrap command before it
	// returns the server's address to containerd. An error here leaves the
	// started shim server running.
	afterStartShim = "AfterStartShim"
)

func main() {
	shim.RunShim(context.Background(), &shimWithStartFailpoint{
		Shim: manager.NewShimManager("io.containerd.runc-fp.v1"),
	})
}

type shimWithStartFailpoint struct {
	shim.Shim
}

func (s *shimWithStartFailpoint) Start(ctx context.Context, params *bootapi.BootstrapParams) (*bootapi.BootstrapResult, error) {
	fps, err := newFailpointFromOCIAnnotation()
	if err != nil {
		return nil, err
	}
	if err := evaluateFailpoint(fps, beforeStartShim); err != nil {
		return nil, err
	}
	result, err := s.Shim.Start(ctx, params)
	if err != nil {
		return nil, err
	}
	if err := evaluateFailpoint(fps, afterStartShim); err != nil {
		return nil, err
	}
	return result, nil
}

func evaluateFailpoint(fps map[string]*failpoint.Failpoint, name string) error {
	if fp, ok := fps[name]; ok {
		return fp.Evaluate()
	}
	return nil
}
