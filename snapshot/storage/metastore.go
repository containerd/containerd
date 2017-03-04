package storage

import (
	"context"

	"github.com/docker/containerd/snapshot"
)

// MetaStore is used to store metadata related to a snapshot driver. The
// MetaStore is intended to store metadata related to name, state and
// parentage. Using the MetaStore is not required to implement a snapshot
// driver but can be used to handle the persistence and transactional
// complexities of a driver implementation.
type MetaStore interface {
	// Stat returns the snapshot stat Info directly from
	// the metadata.
	Stat(ctx context.Context, key string) (snapshot.Info, error)

	// Walk iterates through all metadata for the stored
	// snapshots and calls the provided function for each.
	Walk(ctx context.Context, fn func(context.Context, snapshot.Info) error) error

	// CreateActive creates a new active snapshot transaction referenced by
	// the provided key. The provided opts carries the parent and readonly
	// properties to store with the active snapshots. Additionally a create
	// method can be provided in order to allow the caller perform an action
	// with the active snapshots identifier before the metadata is
	// committed. This callback action should be as short as possible to
	// avoid blocking the transaction longer than necessary.
	CreateActive(ctx context.Context, key string, opts CreateActiveOpts) error

	// GetActive returns the metadata for the active snapshot transaction
	// referenced by the given key.
	GetActive(ctx context.Context, key string) (Active, error)

	// Remove removes a snapshot from the metastore. The optional cleanup
	// callback should be treated as the first part in a multi-phase commit,
	// meaning data should be marked for removal but restorable if the
	// database commit does not succeed.
	Remove(ctx context.Context, key string, opts RemoveOpts) error

	// Commit renames the active snapshot transaction referenced by `key`
	// as a committed snapshot referenced by `Name`. The resulting snapshot
	// will be committed and readonly and the `key` reference will no longer
	// be available for lookup or removal. The snapshot identifier given
	// on creation and retrieved from GetActive will not change on commit.
	Commit(ctx context.Context, key string, opts CommitOpts) error
}

// CreateActiveOpts are used to configure the creation of a new active snapshot
// transaction. The Parent and Readonly values are treated as metadata on the
// active snapshot, the Create function is called during creation.
type CreateActiveOpts struct {
	Parent   string
	Readonly bool
	Create   func(Active) error
}

// CommitOpts are used to configure the commit transaction. The given name will
// be the name after the active is committed. The Commit function is called
// during the transaction to allow the caller to rename any resources before
// the final Commit.
type CommitOpts struct {
	Name   string
	Commit func(id string) error
}

// RemoveOpts are used to configure the remove transaction. The provided
// cleanup function will be called during the remove transaction to allow
// the caller to make any resource unavailable before the commit is
// finalized.
type RemoveOpts struct {
	Cleanup func(id string, k snapshot.Kind) error
}

// Active hold the metadata for an active snapshot transaction. The ParentIDs
// hold the snapshot identifiers for the committed snapshots this active is
// based on. The ParentIDs are ordered from the lowest base to highest, meaning
// they should be applied in order from the first index to the last index. The
// last index should always be considered the active snapshots immediate parent.
type Active struct {
	ID        string
	ParentIDs []string
	Readonly  bool
}
