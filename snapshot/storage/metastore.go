package storage

import (
	"context"

	"github.com/containerd/containerd/snapshot"
)

// MetaStore is used to store metadata related to a snapshot driver. The
// MetaStore is intended to store metadata related to name, state and
// parentage. Using the MetaStore is not required to implement a snapshot
// driver but can be used to handle the persistence and transactional
// complexities of a driver implementation.
type MetaStore interface {
	// TransactionContext creates a new transaction context.
	TransactionContext(ctx context.Context, writable bool) (context.Context, Transactor, error)

	// Stat returns the snapshot stat Info directly from
	// the metadata.
	Stat(ctx context.Context, key string) (snapshot.Info, error)

	// Walk iterates through all metadata for the stored
	// snapshots and calls the provided function for each.
	Walk(ctx context.Context, fn func(context.Context, snapshot.Info) error) error

	// CreateActive creates a new active snapshot transaction referenced by
	// the provided key. The new active snapshot will have the provided
	// parent. If the readonly option is given, the active snapshot will be
	// marked as readonly and can only be removed, and not committed. The
	// provided context must contain a writable transaction.
	CreateActive(ctx context.Context, key, parent string, readonly bool) (Active, error)

	// GetActive returns the metadata for the active snapshot transaction
	// referenced by the given key.
	GetActive(ctx context.Context, key string) (Active, error)

	// Remove removes a snapshot from the metastore. The provided context
	// must contain a writable transaction. The string identifier for the
	// snapshot is returned as well as the kind.
	Remove(ctx context.Context, key string) (string, snapshot.Kind, error)

	// Commit renames the active snapshot transaction referenced by `key`
	// as a committed snapshot referenced by `Name`. The resulting snapshot
	// will be committed and readonly. The `key` reference will no longer
	// be available for lookup or removal. The returned string identifier
	// for the committed snapshot is the same identifier of the original
	// active snapshot.
	Commit(ctx context.Context, key, name string) (string, error)
}

// Transactor is used to finalize an active transaction.
type Transactor interface {
	// Commit commits any changes made during the transaction.
	Commit() error

	// Rollback rolls back any changes made during the transaction.
	Rollback() error
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
