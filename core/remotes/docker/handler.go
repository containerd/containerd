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

package docker

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/containerd/v2/pkg/reference"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// AppendDistributionSourceLabel updates the label of blob with distribution source.
func AppendDistributionSourceLabel(manager content.Manager, ref string) (images.HandlerFunc, error) {
	refspec, err := reference.Parse(ref)
	if err != nil {
		return nil, err
	}

	u, err := url.Parse("dummy://" + refspec.Locator)
	if err != nil {
		return nil, err
	}

	source, repo := u.Hostname(), strings.TrimPrefix(u.Path, "/")
	key := distributionSourceLabelKey(source)
	return func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		return nil, applyDistributionSourceLabels(ctx, manager, desc, map[string]string{key: repo})
	}, nil
}

// DistributionSourceHandler is a handler wrapper that applies distribution
// source annotations from each descriptor to the corresponding content labels,
// and propagates them to child descriptors.
//
// During OCI image export, distribution source labels are preserved as
// annotations on the top-level index descriptors. This handler restores them
// as content labels during import and propagates them down the content tree
// so that all children (manifests, configs, layers) are labeled correctly.
func DistributionSourceHandler(manager content.Manager, f images.HandlerFunc) images.HandlerFunc {
	return func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		children, err := f(ctx, desc)
		if err != nil {
			return children, err
		}

		srcLabels := distributionSourceAnnotations(desc.Annotations)
		if len(srcLabels) == 0 {
			return children, nil
		}

		if err := applyDistributionSourceLabels(ctx, manager, desc, srcLabels); err != nil {
			if !errdefs.IsNotFound(err) {
				return nil, err
			}
		}

		// Propagate distribution source annotations to children.
		for i := range children {
			for k, v := range srcLabels {
				if children[i].Annotations == nil {
					children[i].Annotations = map[string]string{}
				}
				if _, exists := children[i].Annotations[k]; !exists {
					children[i].Annotations[k] = v
				}
			}
		}

		return children, nil
	}
}

func distributionSourceAnnotations(annotations map[string]string) map[string]string {
	var result map[string]string
	for k, v := range annotations {
		if strings.HasPrefix(k, labels.LabelDistributionSource+".") {
			if result == nil {
				result = map[string]string{}
			}
			result[k] = v
		}
	}
	return result
}

func applyDistributionSourceLabels(ctx context.Context, manager content.Manager, desc ocispec.Descriptor, srcLabels map[string]string) error {
	info, err := manager.Info(ctx, desc.Digest)
	if err != nil {
		return err
	}

	updateLabels := map[string]string{}
	var fields []string
	for key, repo := range srcLabels {
		originLabel := ""
		if info.Labels != nil {
			originLabel = info.Labels[key]
		}
		value := appendDistributionSourceLabel(originLabel, repo)

		// The repo name has been limited under 256 and the distribution
		// label might hit the limitation of label size, when blob data
		// is used as the very, very common layer.
		if err := labels.Validate(key, value); err != nil {
			log.G(ctx).Warnf("skip to append distribution label: %s", err)
			continue
		}

		updateLabels[key] = value
		fields = append(fields, fmt.Sprintf("labels.%s", key))
	}

	if len(fields) == 0 {
		return nil
	}

	_, err = manager.Update(ctx, content.Info{
		Digest: desc.Digest,
		Labels: updateLabels,
	}, fields...)
	return err
}

func appendDistributionSourceLabel(originLabel, repo string) string {
	repos := []string{}
	if originLabel != "" {
		repos = strings.Split(originLabel, ",")
	}
	repos = append(repos, repo)

	// use empty string to present duplicate items
	for i := 1; i < len(repos); i++ {
		tmp, j := repos[i], i-1
		for ; j >= 0 && repos[j] >= tmp; j-- {
			if repos[j] == tmp {
				tmp = ""
			}
			repos[j+1] = repos[j]
		}
		repos[j+1] = tmp
	}

	i := 0
	for ; i < len(repos) && repos[i] == ""; i++ {
	}

	return strings.Join(repos[i:], ",")
}

func distributionSourceLabelKey(source string) string {
	return labels.LabelDistributionSource + "." + source
}

// selectRepositoryMountCandidate will select the repo which has longest
// common prefix components as the candidate.
func selectRepositoryMountCandidate(refspec reference.Spec, sources map[string]string) string {
	u, err := url.Parse("dummy://" + refspec.Locator)
	if err != nil {
		// NOTE: basically, it won't be error here
		return ""
	}

	source, target := u.Hostname(), strings.TrimPrefix(u.Path, "/")
	repoLabel, ok := sources[distributionSourceLabelKey(source)]
	if !ok || repoLabel == "" {
		return ""
	}

	n, match := 0, ""
	components := strings.Split(target, "/")
	for repo := range strings.SplitSeq(repoLabel, ",") {
		// the target repo is not a candidate
		if repo == target {
			continue
		}

		if l := commonPrefixComponents(components, repo); l >= n {
			n, match = l, repo
		}
	}
	return match
}

func commonPrefixComponents(components []string, target string) int {
	targetComponents := strings.Split(target, "/")

	i := 0
	for ; i < len(components) && i < len(targetComponents); i++ {
		if components[i] != targetComponents[i] {
			break
		}
	}
	return i
}
