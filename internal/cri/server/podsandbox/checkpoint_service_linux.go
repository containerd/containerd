//go:build linux

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
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/errdefs"
)

// CheckpointService contains the pause-controller implementation of Pod
// checkpoint and restore. Its only external inputs are sandbox controller
// option values. It does not depend on CRI stores or lifecycle callbacks.
type CheckpointService struct {
	client   *client.Client
	rootDir  string
	shimPath string

	podCheckpointState
}

type CheckpointServiceOptions struct {
	Client  *client.Client
	RootDir string
	// ShimPath is the custom PATH environment variable from the shim manager.
	ShimPath string
}

func NewCheckpointService(options CheckpointServiceOptions) (*CheckpointService, error) {
	if options.Client == nil {
		return nil, errors.New("checkpoint service requires a containerd client")
	}
	if options.RootDir == "" {
		return nil, errors.New("checkpoint service requires a root directory")
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
	service := &CheckpointService{
		client:   options.Client,
		rootDir:  options.RootDir,
		shimPath: options.ShimPath,
	}
	if err := service.removeStaleMarkerTemps(); err != nil {
		return nil, fmt.Errorf("failed to remove stale checkpoint recovery markers: %w", err)
	}
	return service, nil
}

// Restore images carry their sandbox owner before writable-layer preparation.
// Teardown can therefore find imports even if CRI never received their names.
const restoreSandboxLabel = "io.containerd.cri.restore.sandbox"

func (c *CheckpointService) cleanupRestoreImages(ctx context.Context, sandboxID string) error {
	filter := fmt.Sprintf("labels.%q==%q", restoreSandboxLabel, sandboxID)
	prepared, err := c.client.ImageService().List(ctx, filter)
	if err != nil {
		return fmt.Errorf("list restore images for sandbox %q: %w", sandboxID, err)
	}
	var cleanupErrors []error
	for _, image := range prepared {
		if err := c.client.ImageService().Delete(ctx, image.Name, images.SynchronousDelete()); err != nil && !errdefs.IsNotFound(err) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove restore image %q: %w", image.Name, err))
		}
	}
	return errors.Join(cleanupErrors...)
}
