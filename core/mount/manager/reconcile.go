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
	"path/filepath"
	"runtime"

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
// once, for an entire collection pass to share, rather than probing
// each record individually the way probeMounted does for Activate:
// reconciliation only ever needs to ask "is this path mounted", never
// "mount this", so one bulk read replaces what would otherwise be one
// host mount table parse per handler-less record.
//
// The returned bool is false when the result cannot be trusted at
// all, in which case the map is always nil: canObserveMountTableOS
// says so outright, or reading the table failed for this pass. A
// handler-less mount is not reconciled in either case; see
// reconcileLive.
func (mm *mountManager) snapshotMountTable(ctx context.Context) (map[string]struct{}, bool) {
	if !canObserveMountTableOS {
		return nil, false
	}
	prefix := filepath.Clean(mm.targets.Name())
	infos, err := mountinfo.GetMounts(mountinfo.PrefixFilter(prefix))
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

// reconcileLive reports whether path is actually mounted, trusted
// completely: unlike probeMounted, whose caller redoes a mount on a
// false negative, this is used to decide whether to discard a record
// outright, so a wrong answer here does not waste work, it deletes
// something live.
//
// A Handler implementing mount.MountedChecker is always asked
// directly, on any platform: its answer does not depend on this
// package's own ability to read the host mount table. Any other
// mount, handler-less or otherwise, is checked against mounted, the
// snapshot from snapshotMountTable, or assumed live if haveMountTable
// is false: see mount.MountedChecker's doc for why not implementing
// it is itself a claim that the generic check is accurate, and
// snapshotMountTable's doc for when that check cannot be trusted at
// all.
func reconcileLive(ctx context.Context, handler mount.Handler, path string, mounted map[string]struct{}, haveMountTable bool) (bool, error) {
	if mc, ok := handler.(mount.MountedChecker); ok {
		return mc.Mounted(ctx, path)
	}
	if !haveMountTable {
		return true, nil
	}
	_, ok := mounted[path]
	return ok, nil
}

// activationLive reports whether every position in a v2 activation's
// chain is actually mounted. An activation with no managed positions
// at all is vacuously live, the same rule staleCollision applies for
// the same case: the two must never disagree about what "live" means
// for an activation neither invented.
func activationLive(ctx context.Context, mm *mountManager, nsbkt, bkt *bolt.Bucket, mounted map[string]struct{}, haveMountTable bool) (bool, error) {
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
			// Defensive only, matching staleCollision: a record is
			// never deleted while anything still uses it, so a
			// surviving activation should never reference one that
			// is gone.
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
// activation's chain is actually mounted, matching staleCollision's
// own v1 rule exactly: no active bucket at all means the activation
// was interrupted before it ever completed, which is never live, not
// vacuously live the way an activation with an active bucket but no
// positions in it would be. v1Positions already returns nil in both
// of those cases, so the two can only be told apart by asking
// v1HasActive first, not by looking at the position list alone.
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
