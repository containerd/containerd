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

package content

import (
	"context"
	"math/rand"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func TestKmutex(t *testing.T) {
	t.Parallel()

	km := newKmutex()
	ctx := context.Background()

	km.lock(ctx, "c1")
	km.lock(ctx, "c2")

	assert.Check(t, is.Equal(len(km.locks), 2))
	assert.Check(t, is.Equal(km.locks["c1"].ref, 1))
	assert.Check(t, is.Equal(km.locks["c2"].ref, 1))

	waitCh := make(chan struct{})
	go func() {
		defer close(waitCh)

		km.lock(ctx, "c1")
	}()

	retries := 100
	waitLock := false
	for i := 0; i < retries; i++ {
		// prevent from data-race
		km.mu.Lock()
		c1Ref := km.locks["c1"].ref
		km.mu.Unlock()

		if c1Ref == 2 {
			waitLock = true
			break
		}
		time.Sleep(time.Duration(rand.Int63n(100)) * time.Millisecond)
	}

	assert.Check(t, is.Equal(waitLock, true))
	km.unlock("c1")

	<-waitCh
	assert.Check(t, is.Equal(km.locks["c1"].ref, 1))
}

func TestKmutexUnlockPanic(t *testing.T) {
	t.Parallel()

	km := newKmutex()

	defer func() {
		if recover() == nil {
			t.Fatal("unlock of unlocked key did not panic")
		}
	}()

	km.unlock(t.Name())
}

func TestKmutexMultiLockOnKeys(t *testing.T) {
	t.Parallel()

	km := newKmutex()
	nproc := runtime.GOMAXPROCS(0)
	nloops := 10000

	var wg sync.WaitGroup
	for i := 0; i < nproc; i++ {
		wg.Add(1)

		go func(key string) {
			defer wg.Done()

			ctx := context.Background()
			for i := 0; i < nloops; i++ {
				km.lock(ctx, key)

				time.Sleep(time.Duration(rand.Int63n(100)) * time.Nanosecond)

				km.unlock(key)
			}
		}(strconv.Itoa(i))
	}
	wg.Wait()
}

func TestKmutexMultiLockOnSameKey(t *testing.T) {
	t.Parallel()

	km := newKmutex()
	key := "c1"

	assert.NilError(t, km.lock(context.Background(), key))

	// cancel should cancel waiting lock
	func() {
		var (
			ackCh = make(chan struct{}, 1)
			errCh = make(chan error, 1)
		)

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			ackCh <- struct{}{}
			errCh <- km.lock(ctx, key)
		}()

		<-ackCh
		time.Sleep(time.Duration(rand.Int63n(100)) * time.Millisecond)
		cancel()
		assert.Check(t, is.Equal(<-errCh, context.Canceled))
	}()

	nproc := runtime.GOMAXPROCS(0)
	nloops := 10000

	var wg sync.WaitGroup
	for i := 0; i < nproc; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			ctx := context.Background()
			for i := 0; i < nloops; i++ {
				km.lock(ctx, key)

				time.Sleep(time.Duration(rand.Int63n(100)) * time.Nanosecond)

				km.unlock(key)
			}
		}()
	}
	km.unlock(key)
	wg.Wait()

	// c1 key has been released so the km should not have any klock.
	assert.Check(t, is.Equal(len(km.locks), 0))
}
