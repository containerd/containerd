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

package kmutex

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/semaphore"
)

const maxRWReaders int64 = 1 << 30

// KeyedRWLocker provides shared and exclusive locks scoped by key.
type KeyedRWLocker interface {
	KeyedLocker
	RLock(context.Context, string) error
	RUnlock(string)
}

// NewRW creates a keyed reader/writer lock.
func NewRW() KeyedRWLocker {
	return &keyRWMutex{locks: make(map[string]*keyRWLock)}
}

type keyRWMutex struct {
	mu    sync.Mutex
	locks map[string]*keyRWLock
}

type keyRWLock struct {
	*semaphore.Weighted
	ref int
}

func (km *keyRWMutex) Lock(ctx context.Context, key string) error {
	return km.acquire(ctx, key, maxRWReaders)
}

func (km *keyRWMutex) Unlock(key string) {
	km.release(key, maxRWReaders)
}

func (km *keyRWMutex) RLock(ctx context.Context, key string) error {
	return km.acquire(ctx, key, 1)
}

func (km *keyRWMutex) RUnlock(key string) {
	km.release(key, 1)
}

func (km *keyRWMutex) acquire(ctx context.Context, key string, weight int64) error {
	km.mu.Lock()
	l, ok := km.locks[key]
	if !ok {
		l = &keyRWLock{Weighted: semaphore.NewWeighted(maxRWReaders)}
		km.locks[key] = l
	}
	l.ref++
	km.mu.Unlock()

	if err := l.Acquire(ctx, weight); err != nil {
		km.mu.Lock()
		l.ref--
		if l.ref == 0 {
			delete(km.locks, key)
		}
		km.mu.Unlock()
		return err
	}
	return nil
}

func (km *keyRWMutex) release(key string, weight int64) {
	km.mu.Lock()
	defer km.mu.Unlock()
	l, ok := km.locks[key]
	if !ok {
		panic(fmt.Errorf("unlock of unlocked key %q", key))
	}
	l.Release(weight)
	l.ref--
	if l.ref == 0 {
		delete(km.locks, key)
	}
}
