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

package manager

import (
	"context"
	"slices"
	"strings"

	"github.com/containerd/log"

	"github.com/containerd/containerd/v2/core/mount"
)

// activationPlan is the result of deciding, for a set of mounts, which
// transforms the mount manager must apply itself and which mounts it must
// perform, honoring the mount types and transforms the caller has claimed.
type activationPlan struct {
	// firstSystemMount is the index of the first mount to return to the
	// caller as a system mount, rather than perform inside the mount
	// manager. -1 means nothing needs the manager at all: the caller should
	// perform every mount itself, unmodified.
	firstSystemMount int

	// transforms[i] are the transforms recorded for mounts[i], in
	// application order (outermost transform first, so the one nearest the
	// original mount type comes first). nil if mounts[i] had no transform
	// prefix.
	transforms [][]mount.Transformer

	// handlers[i] is the Handler to use for mounts[i], or nil to perform a
	// plain system mount.
	handlers []mount.Handler
}

// planActivation decides, for each mount, which transforms the mount manager
// must apply and whether the manager or the caller performs the mount
// itself, honoring the mount types and transforms config.AllowMountTypes and
// config.AllowTransforms claim.
func planActivation(ctx context.Context, mounts []mount.Mount, config mount.ActivateOptions, transforms map[string]mount.Transformer, handlers map[string]mount.Handler) activationPlan {
	shouldTransform := func(p string, t string) bool {
		return !slices.Contains(config.AllowTransforms, p) && !slices.Contains(config.AllowMountTypes, t)
	}
	shouldHandle := func(t string) bool {
		return !slices.Contains(config.AllowMountTypes, t)
	}

	plan := activationPlan{firstSystemMount: -1}

	for i := range mounts {
		mountType := mounts[i].Type

		// Check is the source needs transformation, any transform operation requires
		// mounting with the mount manager.
		for transformType, mt, ok := strings.Cut(mountType, "/"); ok; transformType, mt, ok = strings.Cut(mountType, "/") {
			tr, ok := transforms[transformType]
			if !ok {
				log.G(ctx).Warnf("unknown transform %q for mount %v", transformType, mounts[i]) //nolint:gosec // G602: i is bounded by range mounts
				break
			}

			if shouldTransform(transformType, mounts[i].Type) { //nolint:gosec // G602: i is bounded by range mounts
				// At least everything before this must be mounted
				// by the mount manager
				plan.firstSystemMount = i
			}

			if plan.handlers == nil {
				plan.handlers = make([]mount.Handler, len(mounts))
			}
			if plan.transforms == nil {
				plan.transforms = make([][]mount.Transformer, len(mounts))
			}

			plan.transforms[i] = append(plan.transforms[i], typeTransformer{
				Transformer: tr,
				mountType:   mt,
			})

			mountType = mt
		}

		var handler mount.Handler
		if handlers != nil {
			handler = handlers[mountType]
		}

		if handler != nil || config.Temporary {
			if plan.handlers == nil {
				plan.handlers = make([]mount.Handler, len(mounts))
			}
			plan.handlers[i] = handler
			if shouldHandle(mountType) || config.Temporary {
				plan.firstSystemMount = i + 1
			}
		}
	}

	return plan
}
