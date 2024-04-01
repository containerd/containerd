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
	"path"
	"path/filepath"

	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	docker "github.com/distribution/reference"
	imagedigest "github.com/opencontainers/go-digest"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	crilabels "github.com/containerd/containerd/v2/internal/cri/labels"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
	clabels "github.com/containerd/containerd/v2/pkg/labels"
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

// getRepoDigestAngTag returns image repoDigest and repoTag of the named image reference.
func getRepoDigestAndTag(namedRef docker.Named, digest imagedigest.Digest, schema1 bool) (string, string) {
	var repoTag, repoDigest string
	if _, ok := namedRef.(docker.NamedTagged); ok {
		repoTag = namedRef.String()
	}
	if _, ok := namedRef.(docker.Canonical); ok {
		repoDigest = namedRef.String()
	} else if !schema1 {
		// digest is not actual repo digest for schema1 image.
		repoDigest = namedRef.Name() + "@" + digest.String()
	}
	return repoDigest, repoTag
}

// toContainerdImage converts an image object in image store to containerd image handler.
func (c *Controller) toContainerdImage(ctx context.Context, image imagestore.Image) (containerd.Image, error) {
	// image should always have at least one reference.
	if len(image.References) == 0 {
		return nil, fmt.Errorf("invalid image with no reference %q", image.ID)
	}
	return c.client.GetImage(ctx, image.References[0])
}

// buildLabel builds the labels from config to be passed to containerd
func buildLabels(configLabels, imageConfigLabels map[string]string, containerType string) map[string]string {
	labels := make(map[string]string)

	for k, v := range imageConfigLabels {
		if err := clabels.Validate(k, v); err == nil {
			labels[k] = v
		} else {
			// In case the image label is invalid, we output a warning and skip adding it to the
			// container.
			log.L.WithError(err).Warnf("unable to add image label with key %s to the container", k)
		}
	}
	// labels from the CRI request (config) will override labels in the image config
	for k, v := range configLabels {
		labels[k] = v
	}
	labels[crilabels.ContainerKindLabel] = containerType
	return labels
}

// parseImageReferences parses a list of arbitrary image references and returns
// the repotags and repodigests
func parseImageReferences(refs []string) ([]string, []string) {
	var tags, digests []string
	for _, ref := range refs {
		parsed, err := docker.ParseAnyReference(ref)
		if err != nil {
			continue
		}
		if _, ok := parsed.(docker.Canonical); ok {
			digests = append(digests, parsed.String())
		} else if _, ok := parsed.(docker.Tagged); ok {
			tags = append(tags, parsed.String())
		}
	}
	return tags, digests
}

// getPassthroughAnnotations filters requested pod annotations by comparing
// against permitted annotations for the given runtime.
func getPassthroughAnnotations(podAnnotations map[string]string,
	runtimePodAnnotations []string) (passthroughAnnotations map[string]string) {
	passthroughAnnotations = make(map[string]string)

	for podAnnotationKey, podAnnotationValue := range podAnnotations {
		for _, pattern := range runtimePodAnnotations {
			// Use path.Match instead of filepath.Match here.
			// filepath.Match treated `\\` as path separator
			// on windows, which is not what we want.
			if ok, _ := path.Match(pattern, podAnnotationKey); ok {
				passthroughAnnotations[podAnnotationKey] = podAnnotationValue
			}
		}
	}
	return passthroughAnnotations
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
