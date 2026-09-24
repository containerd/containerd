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
	"os"
	"path/filepath"
	"runtime"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/moby/sys/mountinfo"

	"github.com/containerd/log"

	"github.com/containerd/containerd/v2/core/mount"
)

// This file reconciles the activations a database records against the
// mounts actually in effect, the direction neither GC's own reference
// counting nor the orphan directory scans in manager.go/v1.go cover:
// an activation whose write transaction committed but which never got
// (or no longer has) the mounts it describes, because the process
// died in between, or because something outside this package tore a
// mount down. Every collection pass releases what it finds, exactly
// as if a caller had deactivated it: a name recorded but not actually
// mounted is otherwise reported by Info and List, and blocks Activate
// with ErrAlreadyExists, indefinitely, until something reactivates it.
//
// canObserveMountTableOS is false exactly where the host's mount
// table cannot be trusted to distinguish "not mounted" from "cannot
// tell" at all: on Windows, mountinfo's own fallback silently reports
// every path as unmounted rather than erroring (see probeMounted's
// doc). A handler-less mount is never reconciled while this is false;
// a Handler is always checked directly, on any platform, since
// mount.MountedChecker's contract does not depend on this.
const canObserveMountTableOS = runtime.GOOS != "windows"

// snapshotMountTable reads every mount currently under mm.targets,
// once, for an entire collection pass to share. The returned bool is
// false when the result cannot be trusted at all, in which case the
// map is always nil; see reconcileLive.
func (mm *mountManager) snapshotMountTable(ctx context.Context) (map[string]struct{}, bool) {
	if !canObserveMountTableOS {
		return nil, false
	}
	// mm.targets.Name() is already canonical; see NewManager.
	infos, err := mountinfo.GetMounts(mountinfo.PrefixFilter(mm.targets.Name()))
	if err != nil {
		log.G(ctx).WithError(err).Warn("failed to read host mount table; handler-less mounts will not be reconciled this pass")
		return nil, false
	}
	mounted := make(map[string]struct{}, len(infos))
	for _, info := range infos {
		mounted[info.Mountpoint] = struct{}{}
	}
	return mounted, true
}

// reconcileLive reports whether path is actually mounted: via
// handler's MountedChecker if it implements one, otherwise against
// mounted, or assumed live if haveMountTable is false.
func reconcileLive(ctx context.Context, handler mount.Handler, path string, mounted map[string]struct{}, haveMountTable bool) (bool, error) {
	if mc, ok := handler.(mount.MountedChecker); ok {
		return mc.Mounted(ctx, path)
	}
	if !haveMountTable {
		return true, nil
	}
	if _, ok := mounted[path]; ok {
		return true, nil
	}
	// A v1 position may still carry the unresolved spelling a pre-v2
	// binary wrote it with; mounted only ever holds resolved paths.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		_, ok := mounted[resolved]
		return ok, nil
	}
	return false, nil
}

// activationLive reports whether every position in a v2 activation's
// chain is actually mounted, and every recorded ensure target exists.
func activationLive(ctx context.Context, mm *mountManager, nsbkt, bkt *bolt.Bucket, mounted map[string]struct{}, haveMountTable bool) (bool, error) {
	if v := bkt.Get(bucketKeyEnsureTargets); len(v) > 0 {
		for target := range strings.SplitSeq(string(v), "\x00") {
			if _, err := os.Stat(target); err != nil {
				if !os.IsNotExist(err) {
					return false, err
				}
				return false, nil
			}
		}
	}
	ids := activationUses(bkt)
	if len(ids) == 0 {
		return true, nil
	}
	for _, id := range ids {
		b, ok, err := getMountedRecord(nsbkt, mountedKey(id))
		if err != nil {
			return false, err
		}
		if !ok {
			// Should not be reachable; treat as not live.
			return false, nil
		}
		live, err := reconcileLive(ctx, mm.handlers[b.mount.Type], b.point, mounted, haveMountTable)
		if err != nil {
			return false, err
		}
		if !live {
			return false, nil
		}
	}
	return true, nil
}

// v1ActivationLive reports whether every position in a v1
// activation's chain is actually mounted. No active bucket at all
// means the activation was interrupted and is never live.
func v1ActivationLive(ctx context.Context, mm *mountManager, bkt *bolt.Bucket, mounted map[string]struct{}, haveMountTable bool) (bool, error) {
	if !v1HasActive(bkt) {
		return false, nil
	}
	for _, p := range v1Positions(bkt) {
		live, err := reconcileLive(ctx, mm.handlers[p.mtype], p.point, mounted, haveMountTable)
		if err != nil {
			return false, err
		}
		if !live {
			return false, nil
		}
	}
	return true, nil
}
