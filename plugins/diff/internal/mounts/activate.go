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

package mounts

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"slices"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"

	"github.com/containerd/containerd/v2/core/mount"
)

// Activate activates mounts with the mount manager and returns the system
// mounts to use in their place, along with a cleanup function. Mounts are
// returned unchanged if there is no mount manager or nothing to activate.
func Activate(ctx context.Context, mm mount.Manager, prefix string, mounts []mount.Mount) ([]mount.Mount, func(), error) {
	noop := func() {}
	if mm == nil || len(mounts) <= 1 {
		return mounts, noop, nil
	}

	var b [3]byte
	rand.Read(b[:])
	id := fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixNano(), base64.URLEncoding.EncodeToString(b[:]))

	// Activate may modify the slice, do not change the caller's mounts
	info, err := mm.Activate(ctx, id, slices.Clone(mounts))
	if errdefs.IsNotImplemented(err) {
		return mounts, noop, nil
	} else if err != nil {
		return nil, nil, fmt.Errorf("failed to activate mounts: %w", err)
	}

	deactivate := func() {
		if err := mm.Deactivate(context.WithoutCancel(ctx), id); err != nil {
			log.G(ctx).WithError(err).WithField("name", id).Warn("failed to deactivate mounts")
		}
	}
	if len(info.System) == 0 {
		deactivate()
		return nil, nil, fmt.Errorf("no system mounts after activating %q: %w", id, errdefs.ErrUnknown)
	}
	return info.System, deactivate, nil
}
