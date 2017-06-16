package content

import (
	"sync"

	"github.com/pkg/errors"
)

// Handles locking references
// TODO: use boltdb for lock status

var (
	// locks lets us lock in process
	locks   = map[string]struct{}{}
	locksMu sync.Mutex
)

func tryLock(ref string) error {
	locksMu.Lock()
	defer locksMu.Unlock()

	if _, ok := locks[ref]; ok {
		return errors.Wrapf(ErrLocked, "key %s is locked", ref)
	}

	locks[ref] = struct{}{}
	return nil
}

func unlock(ref string) {
	locksMu.Lock()
	defer locksMu.Unlock()

	if _, ok := locks[ref]; ok {
		delete(locks, ref)
	}
}
