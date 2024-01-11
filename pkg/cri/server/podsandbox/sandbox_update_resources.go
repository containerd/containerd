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
	"fmt"
	"github.com/containerd/containerd/v2/namespaces"
	"path/filepath"

	"github.com/containerd/containerd/v2/errdefs"
	runtime "github.com/containerd/containerd/v2/runtime/v2"
	"github.com/containerd/containerd/v2/sandbox"
	"github.com/containerd/typeurl/v2"
)

func (c *Controller) UpdateResource(ctx context.Context, sandboxID string, req sandbox.TaskResources) error {
	return nil
}

func (c *Controller) addTask(ctx context.Context, sandboxID string, taskID string, spec typeurl.Any) (string, error) {
	sb := c.store.Get(sandboxID)
	if sb == nil {
		return "", fmt.Errorf("no sandbox found %w", errdefs.ErrNotFound)
	}
	sandboxStateDir := c.getVolatileSandboxRootDir(sandboxID)
	sandboxRootDir := c.getSandboxRootDir(sandboxID)
	stateDir := filepath.Join(sandboxStateDir, taskID)
	rootDir := filepath.Join(sandboxRootDir, taskID)
	bundle, err := runtime.NewBundle(ctx, rootDir, stateDir, taskID, spec)
	if err != nil {
		return "", err
	}
	return bundle.Path, nil
}

func (c *Controller) removeTask(ctx context.Context, sandboxID string, taskID string) error {
	ns, exist := namespaces.Namespace(ctx)
	if !exist {
		return fmt.Errorf("no namespace for task  %s when removing", taskID)
	}
	sandboxStateDir := c.getVolatileSandboxRootDir(sandboxID)
	stateDir := filepath.Join(sandboxStateDir, taskID)
	bundle := runtime.Bundle{
		ID:        taskID,
		Path:      stateDir,
		Namespace: ns,
	}
	if err := bundle.Delete(); err != nil {
		return err
	}
	return nil
}
