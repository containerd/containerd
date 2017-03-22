package snapshot

import "github.com/pkg/errors"

var (
	// ErrSnapshotNotExist is returned when a snapshot cannot be found
	ErrSnapshotNotExist = errors.New("snapshot does not exist")

	// ErrSnapshotExist is returned when an operation to create a snapshot
	// encounters a snapshot with the same key
	ErrSnapshotExist = errors.New("snapshot already exists")

	// ErrSnapshotNotActive is returned when a request which requires an
	// active snapshot encounters a non-active snapshot.
	ErrSnapshotNotActive = errors.New("snapshot is not active")

	// ErrSnapshotNotCommitted is returned when a request which requires a
	// committed snapshot encounters a non-committed snapshot.
	ErrSnapshotNotCommitted = errors.New("snapshot is not committed")
)

// IsNotExist returns whether the error represents that a snapshot
// was not found.
func IsNotExist(err error) bool {
	return errors.Cause(err) == ErrSnapshotNotExist
}

// IsExist returns whether the error represents whether a snapshot
// already exists using a provided key.
func IsExist(err error) bool {
	return errors.Cause(err) == ErrSnapshotExist
}

// IsNotActive returns whether the error represents a request
// for a non active snapshot when an active snapshot is expected.
func IsNotActive(err error) bool {
	return errors.Cause(err) == ErrSnapshotNotActive
}

// IsNotCommitted returns whether the error represents a request
// for a non committed snapshot when a committed snapshot is expected.
func IsNotCommitted(err error) bool {
	return errors.Cause(err) == ErrSnapshotNotCommitted
}
