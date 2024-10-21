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
	"path/filepath"

	"github.com/containerd/typeurl/v2"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	crilabels "github.com/containerd/containerd/v2/internal/cri/labels"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/containerd/v2/pkg/oci"
)

const (

	// sandboxesDir contains all sandbox root. A sandbox root is the running
	// directory of the sandbox, all files created for the sandbox will be
	// placed under this directory.
	sandboxesDir = "sandboxes"
	// MetadataKey is the key used for storing metadata in the sandbox extensions
	MetadataKey = "metadata"
)

const (
	// unknownExitCode is the exit code when exit reason is unknown.
	unknownExitCode = 255
)

// getSandboxRootDir returns the root directory for managing sandbox files,
// e.g. hosts files.
func (c *Controller) getSandboxRootDir(id string) string {
	return filepath.Join(c.config.RootDir, sandboxesDir, id)
}

// getVolatileSandboxRootDir returns the root directory for managing volatile sandbox files,
// e.g. named pipes.
func (c *Controller) getVolatileSandboxRootDir(id string) string {
	return filepath.Join(c.config.StateDir, sandboxesDir, id)
}

// toContainerdImage converts an image object in image store to containerd image handler.
func (c *Controller) toContainerdImage(ctx context.Context, image imagestore.Image) (containerd.Image, error) {
	// image should always have at least one reference.
	if len(image.References) == 0 {
		return nil, fmt.Errorf("invalid image with no reference %q", image.ID)
	}
	return c.client.GetImage(ctx, image.References[0])
}

// runtimeSpec returns a default runtime spec used in cri-containerd.
func (c *Controller) runtimeSpec(id string, baseSpecFile string, opts ...oci.SpecOpts) (*runtimespec.Spec, error) {
	// GenerateSpec needs namespace.
	ctx := ctrdutil.NamespacedContext()
	container := &containers.Container{ID: id}

	if baseSpecFile != "" {
		baseSpec, err := c.runtimeService.LoadOCISpec(baseSpecFile)
		if err != nil {
			return nil, fmt.Errorf("can't load base OCI spec %q: %w", baseSpecFile, err)
		}

		spec := oci.Spec{}
		if err := ctrdutil.DeepCopy(&spec, &baseSpec); err != nil {
			return nil, fmt.Errorf("failed to clone OCI spec: %w", err)
		}

		// Fix up cgroups path
		applyOpts := append([]oci.SpecOpts{oci.WithNamespacedCgroup()}, opts...)

		if err := oci.ApplyOpts(ctx, nil, container, &spec, applyOpts...); err != nil {
			return nil, fmt.Errorf("failed to apply OCI options: %w", err)
		}

		return &spec, nil
	}

	spec, err := oci.GenerateSpec(ctx, nil, container, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to generate spec: %w", err)
	}

	return spec, nil
}

func getMetadata(ctx context.Context, container containerd.Container) (*sandboxstore.Metadata, error) {
	// Load sandbox metadata.
	exts, err := container.Extensions(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox container extensions: %w", err)
	}
	ext, ok := exts[crilabels.SandboxMetadataExtension]
	if !ok {
		return nil, fmt.Errorf("metadata extension %q not found", crilabels.SandboxMetadataExtension)
	}
	data, err := typeurl.UnmarshalAny(ext)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata extension %q: %w", ext, err)
	}
	meta, ok := data.(*sandboxstore.Metadata)
	if !ok {
		return nil, fmt.Errorf("failed to convert the extension to sandbox metadata")
	}
	return meta, nil
}
