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

// Package manager is used to manage mounts in a bolt database, normally
// backed by a tempfs.
//
// The top level bucket name is the schema version. A structural,
// backwards incompatible change to the schema is expressed by moving
// to a new name rather than by migrating data within the old one:
// "v2" is a distinct bucket from "v1", so a binary which only
// understands "v1" neither reads nor writes anything under "v2". Each
// creates its own bucket on first use.
//
// "v1" is read and released but never written or converted; see
// v1.go. An older binary therefore always finds "v1" exactly as it
// left it, minus whatever activations were released in the meantime,
// so a rollback needs no compatibility code. Mounts recorded only
// under "v2" are not something an older binary can restore, but the
// database is expected to live on transient storage (normally a
// tmpfs under the state directory) and every caller of Activate
// treats an unrecognized name as not yet activated and activates it
// again, so a rollback loses the record of those mounts without
// corrupting anything.
//
// Every mount the manager performs is recorded once, as a mounted
// record, and referenced by the activations using it: two activations
// which describe the same mount use the same record and share the
// underlying filesystem, so it stays mounted while either of them
// uses it. This applies uniformly; a mount which cannot be shared with
// another activation still gets its own record, it is simply never
// looked up by another activation's mount parameters.
//
// An activation's entire chain of records is resolved and made
// durable in a single write transaction, before anything is actually
// mounted: mntpoint is computed rather than assigned once mounting
// succeeds, and mntat is approximated as the time resolution ran
// rather than measured after the fact. This lets the whole chain,
// however long, cost exactly one write transaction to activate (two
// if the name collides with a stale record left by a crash, since
// deciding that requires probing the mounts it refers to first).
//
// Because mntpoint and mntat are written before anything is actually
// mounted, neither can be trusted as a record of whether the mount is
// currently live: the manager never assumes a record's mount survived
// a restart, an external unmount, or another operator's intervention,
// and always checks the mount itself (see probeMounted in manager.go)
// before reusing or reporting on a record, repairing it in place if
// the check disagrees with what is recorded. mntpoint and mntat exist
// to say where and roughly when, never whether.
//
// Key names below borrow from two vocabularies, deliberately kept
// apart: an activation and its chain use this package's own terms,
// matching the containerd interface in mount/manager.go
// (ActivationInfo, .Active, .System, .Labels); the mount table an
// activation's chain refers into uses Linux's own terms for the same
// concepts (mnttable, mntid, holders), since a record shared by
// several activations and looked up by identity is the same shape as
// the kernel's own superblock sharing (see sget()), not something
// specific to this package.
/*
Database schema

	v2
	╘══*namespace*
	   ├──activations
	   │  ╘══*mount name*
	   │     ├──id : <varuint64>                  - Unique ID for mount (auto incrementing)
	   │     ├──createdat : <binary time>         - Created at
	   │     ├──updatedat : <binary time>         - Updated at
	   │     ├──lease : <string>                  - Lease
	   │     ├──active                            - The whole chain, resolved in one transaction
	   │     │  ╘══*order*
	   │     │     └──mntid : <varuint64>         - Mount table entry for this position in the chain
	   │     ├──system
	   │     │  ╘══*order*
	   │     │     ├──type : <string>             - Mount type
	   │     │     ├──source : <string>           - Mount source
	   │     │     ├──target : <string>           - Mount target (relative to previous mount point)
	   │     │     └──options : <string>          - NUL separated options
	   │     └──labels
	   │        ╘══*key* : <string>               - Label value
	   ├──mnttable                                - Mounts actually performed by the manager
	   │  ╘══*mount id (varuint64)*
	   │     ├──type : <string>                   - Mount type
	   │     ├──source : <string>                 - Mount source
	   │     ├──target : <string>                 - Mount target
	   │     ├──options : <string>                - NUL separated options
	   │     ├──mntpoint : <string>               - Mount point, computed when resolved
	   │     ├──mntat : <binary time>             - Approximate mount time
	   │     └──holders
	   │        ╘══*mount name* : nil             - Activations using this entry
	   ├──mntindex                                - Dedup index over shareable mount table entries
	   │  ╘══*mount identity digest* : <varuint64>
	   ├──leases
	   │  ╘══*lease id*
	   │     ╘══*mount name*: nil
	   └──unmountq                                (CURRENTLY NOT USED, may remove)
	      └──*mount name + auto-increment*
	         ├──type : <string>                   - Mount type
	         ├──target : <string>                 - Path to check && unmount
	         ├──rm : <bool>                       - Whether to remove target after unmount
	         ├──dev : <string>                    - Device to check before unmount
	         ├──pid : <int>                       - Process to check and kill
	         ├──target : <string>                 - Path to unmount
	         ├──state : <enum>                    - (0 - unmounted, 1 - filesystem, 2 - device, 3 - process)
	         └─ order : <int>                     - Order in which was mounted, unmount high to low

	v1                                            (deprecated: read and released only, see v1.go)
	╘══*namespace*
	   ├──mounts
	   │  ╘══*mount name*
	   │     ├──id : <varuint64>                  - Unique ID (auto incrementing)
	   │     ├──createdat : <binary time>         - Created at
	   │     ├──updatedat : <binary time>         - Updated at
	   │     ├──lease : <string>                  - Lease
	   │     ├──active                            - Absent if never completed
	   │     │  ╘══*order*
	   │     │     ├──type : <string>             - Mount type
	   │     │     ├──mp : <string>               - Mount point
	   │     │     └──mat : <binary time>         - Mount time
	   │     ├──system
	   │     │  ╘══*order*
	   │     │     ├──type : <string>             - Mount type
	   │     │     ├──source : <string>           - Mount source
	   │     │     ├──target : <string>           - Mount target (relative to previous mount point)
	   │     │     └──options : <string>          - NUL separated options
	   │     └──labels
	   │        ╘══*key* : <string>               - Label value
	   └──leases
	      ╘══*lease id*
	         ╘══*mount name*: nil
*/
package manager

var (
	bucketKeyV2 = []byte("v2")
	// bucketKeyV1 is the schema this package replaced. Read and
	// released only, never written; see v1.go, which keeps its own
	// copy of every other key name below rather than sharing these:
	// bucketKeyV1 is the one exception, since code on the "v2" side
	// must be able to name the "v1" bucket itself to fall back into
	// it, which is not the same thing as sharing "v1"'s internal
	// field names.
	bucketKeyV1 = []byte("v1")

	bucketKeyID          = []byte("id")
	bucketKeyActivations = []byte("activations")
	bucketKeyLeases      = []byte("leases")
	bucketKeyLease       = []byte("lease")
	bucketKeyActive      = []byte("active")
	bucketKeySystem      = []byte("system")
	bucketKeyType        = []byte("type")
	bucketKeySource      = []byte("source")
	bucketKeyTarget      = []byte("target")
	bucketKeyOptions     = []byte("options")
	bucketKeyMountedAt   = []byte("mntat")
	bucketKeyMountPoint  = []byte("mntpoint")
	bucketKeyMountTable  = []byte("mnttable")
	bucketKeyMountIndex  = []byte("mntindex")
	bucketKeyMountID     = []byte("mntid")
	bucketKeyHolders     = []byte("holders")
	// bucketKeyLabels is shared with v1.go rather than copied: unlike
	// every other key above, it is not free to ever be renamed, since
	// boltutil.ReadLabels and boltutil.WriteLabels hardcode this exact
	// spelling themselves. There is nothing a future rename here could
	// silently break in "v1", because there is no future in which this
	// one is renamed at all.
	bucketKeyLabels = []byte("labels")

	labelGCContainerBackRef = []byte("containerd.io/gc.bref.container")
	labelGCContentBackRef   = []byte("containerd.io/gc.bref.content")
	labelGCImageBackRef     = []byte("containerd.io/gc.bref.image")
	labelGCSnapBackRef      = []byte("containerd.io/gc.bref.snapshot.")
)
