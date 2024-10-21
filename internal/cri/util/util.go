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

package util

import (
	"context"
	"path"
	"time"

	"github.com/containerd/containerd/v2/internal/cri/constants"
	crilabels "github.com/containerd/containerd/v2/internal/cri/labels"
	clabels "github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/log"
)

// deferCleanupTimeout is the default timeout for containerd cleanup operations
// in defer.
const deferCleanupTimeout = 1 * time.Minute

// DeferContext returns a context for containerd cleanup operations in defer.
// A default timeout is applied to avoid cleanup operation pending forever.
func DeferContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(NamespacedContext(), deferCleanupTimeout)
}

// NamespacedContext returns a context with kubernetes namespace set.
func NamespacedContext() context.Context {
	return WithNamespace(context.Background())
}

// WithNamespace adds kubernetes namespace to the context.
func WithNamespace(ctx context.Context) context.Context {
	return namespaces.WithNamespace(ctx, constants.K8sContainerdNamespace)
}

// GetPassthroughAnnotations filters requested pod annotations by comparing
// against permitted annotations for the given runtime.
func GetPassthroughAnnotations(podAnnotations map[string]string,
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

// BuildLabel builds the labels from config to be passed to containerd
func BuildLabels(configLabels, imageConfigLabels map[string]string, containerType string) map[string]string {
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
