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

package podsandbox

import (
	"fmt"
	"os"
	"sync"

	"github.com/containerd/containerd/v2/client"
)

type CheckpointServiceOptions struct {
	Client  *client.Client
	RootDir string
}

// CheckpointService contains the pause-controller implementation of Pod
// checkpoint and restore. Its only external inputs are sandbox controller
// option values; it does not depend on CRI stores or lifecycle callbacks.
type CheckpointService struct {
	client  *client.Client
	rootDir string

	podCheckpointsInProgress       sync.Map
	podCheckpointOutputsInProgress sync.Map
}

func NewCheckpointService(options CheckpointServiceOptions) (*CheckpointService, error) {
	if options.Client == nil {
		return nil, fmt.Errorf("checkpoint service requires a containerd client")
	}
	if options.RootDir == "" {
		return nil, fmt.Errorf("checkpoint service requires a root directory")
	}
	if err := os.MkdirAll(options.RootDir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create checkpoint service root: %w", err)
	}
	info, err := os.Lstat(options.RootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect checkpoint service root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("checkpoint service root %q is not a real directory", options.RootDir)
	}
	if err := os.Chmod(options.RootDir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to secure checkpoint service root: %w", err)
	}
	return &CheckpointService{
		client:  options.Client,
		rootDir: options.RootDir,
	}, nil
}
