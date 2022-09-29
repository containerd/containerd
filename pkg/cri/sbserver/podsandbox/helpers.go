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
	"strconv"
	"strings"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	clabels "github.com/containerd/containerd/labels"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/oci"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	imagestore "github.com/containerd/containerd/pkg/cri/store/image"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
	runtimeoptions "github.com/containerd/containerd/pkg/runtimeoptions/v1"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/reference/docker"
	"github.com/containerd/containerd/runtime/linux/runctypes"
	runcoptions "github.com/containerd/containerd/runtime/v2/runc/options"
	"github.com/containerd/typeurl"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"

	runhcsoptions "github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/options"
	imagedigest "github.com/opencontainers/go-digest"
	"github.com/pelletier/go-toml"
)

const (

	// sandboxesDir contains all sandbox root. A sandbox root is the running
	// directory of the sandbox, all files created for the sandbox will be
	// placed under this directory.
	sandboxesDir = "sandboxes"
	// criContainerdPrefix is common prefix for cri-containerd
	criContainerdPrefix = "io.cri-containerd"
	// containerKindLabel is a label key indicating container is sandbox container or application container
	containerKindLabel = criContainerdPrefix + ".kind"
	// containerKindSandbox is a label value indicating container is sandbox container
	containerKindSandbox = "sandbox"
	// sandboxMetadataExtension is an extension name that identify metadata of sandbox in CreateContainerRequest
	sandboxMetadataExtension = criContainerdPrefix + ".sandbox.metadata"
	// runtimeRunhcsV1 is the runtime type for runhcs.
	runtimeRunhcsV1 = "io.containerd.runhcs.v1"
)

const (
	// unknownExitCode is the exit code when exit reason is unknown.
	unknownExitCode = 255
)

const (
	handleEventTimeout = 10 * time.Second
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

// getUserFromImage gets uid or username of the image user.
// If user is numeric, it will be treated as uid; or else, it is treated as username.
func getUserFromImage(user string) (*int64, string) {
	// return both empty if user is not specified in the image.
	if user == "" {
		return nil, ""
	}
	// split instances where the id may contain user:group
	user = strings.Split(user, ":")[0]
	// user could be either uid or username. Try to interpret as numeric uid.
	uid, err := strconv.ParseInt(user, 10, 64)
	if err != nil {
		// If user is non numeric, assume it's user name.
		return nil, user
	}
	// If user is a numeric uid.
	return &uid, ""
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
			logrus.WithError(err).Warnf("unable to add image label with key %s to the container", k)
		}
	}
	// labels from the CRI request (config) will override labels in the image config
	for k, v := range configLabels {
		labels[k] = v
	}
	labels[containerKindLabel] = containerType
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

// generateRuntimeOptions generates runtime options from cri plugin config.
func generateRuntimeOptions(r criconfig.Runtime, c criconfig.Config) (interface{}, error) {
	if r.Options == nil {
		if r.Type != plugin.RuntimeLinuxV1 {
			return nil, nil
		}
		// This is a legacy config, generate runctypes.RuncOptions.
		return &runctypes.RuncOptions{
			Runtime:       r.Engine,
			RuntimeRoot:   r.Root,
			SystemdCgroup: c.SystemdCgroup,
		}, nil
	}
	optionsTree, err := toml.TreeFromMap(r.Options)
	if err != nil {
		return nil, err
	}
	options := getRuntimeOptionsType(r.Type)
	if err := optionsTree.Unmarshal(options); err != nil {
		return nil, err
	}
	return options, nil
}

// getRuntimeOptionsType gets empty runtime options by the runtime type name.
func getRuntimeOptionsType(t string) interface{} {
	switch t {
	case plugin.RuntimeRuncV1:
		fallthrough
	case plugin.RuntimeRuncV2:
		return &runcoptions.Options{}
	case plugin.RuntimeLinuxV1:
		return &runctypes.RuncOptions{}
	case runtimeRunhcsV1:
		return &runhcsoptions.Options{}
	default:
		return &runtimeoptions.Options{}
	}
}

// getRuntimeOptions get runtime options from container metadata.
func getRuntimeOptions(c containers.Container) (interface{}, error) {
	from := c.Runtime.Options
	if from == nil || from.GetValue() == nil {
		return nil, nil
	}
	opts, err := typeurl.UnmarshalAny(from)
	if err != nil {
		return nil, err
	}
	return opts, nil
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
		baseSpec, ok := c.baseOCISpecs[baseSpecFile]
		if !ok {
			return nil, fmt.Errorf("can't find base OCI spec %q", baseSpecFile)
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

// Overrides the default snapshotter if Snapshotter is set for this runtime.
// See See https://github.com/containerd/containerd/issues/6657
func (c *Controller) runtimeSnapshotter(ctx context.Context, ociRuntime criconfig.Runtime) string {
	if ociRuntime.Snapshotter == "" {
		return c.config.ContainerdConfig.Snapshotter
	}

	log.G(ctx).Debugf("Set snapshotter for runtime %s to %s", ociRuntime.Type, ociRuntime.Snapshotter)
	return ociRuntime.Snapshotter
}
