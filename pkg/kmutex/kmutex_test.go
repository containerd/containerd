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
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/pkg/randutil"
	"github.com/stretchr/testify/assert"
)

func TestBasic(t *testing.T) {
	t.Parallel()

	km := newKeyMutex()
	ctx := context.Background()

	km.Lock(ctx, "c1")
	km.Lock(ctx, "c2")

	assert.Equal(t, len(km.locks), 2)
	assert.Equal(t, km.locks["c1"].ref, 1)
	assert.Equal(t, km.locks["c2"].ref, 1)

	checkWaitFn := func(key string, num int) {
		retries := 100
		waitLock := false

		for i := 0; i < retries; i++ {
			// prevent from data-race
			km.mu.Lock()
			ref := km.locks[key].ref
			km.mu.Unlock()

			if ref == num {
				waitLock = true
				break
			}
			time.Sleep(time.Duration(randutil.Int63n(100)) * time.Millisecond)
		}
		assert.Equal(t, waitLock, true)
	}

	// should acquire successfully after release
	{
		waitCh := make(chan struct{})
		go func() {
			defer close(waitCh)

			km.Lock(ctx, "c1")
		}()
		checkWaitFn("c1", 2)

		km.Unlock("c1")

		<-waitCh
		assert.Equal(t, km.locks["c1"].ref, 1)
	}

	// failed to acquire if context cancel
	{
		var errCh = make(chan error, 1)

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			errCh <- km.Lock(ctx, "c1")
		}()

		checkWaitFn("c1", 2)

		cancel()
		assert.Equal(t, <-errCh, context.Canceled)
		assert.Equal(t, km.locks["c1"].ref, 1)
	}
}

func TestReleasePanic(t *testing.T) {
	t.Parallel()

	km := newKeyMutex()

	defer func() {
		if recover() == nil {
			t.Fatal("release of unlocked key did not panic")
		}
	}()

	km.Unlock(t.Name())
}

func TestMultileAcquireOnKeys(t *testing.T) {
	t.Parallel()

	km := newKeyMutex()
	nloops := 10000
	nproc := runtime.GOMAXPROCS(0)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < nproc; i++ {
		wg.Add(1)

		go func(key string) {
			defer wg.Done()

			for i := 0; i < nloops; i++ {
				km.Lock(ctx, key)

				time.Sleep(time.Duration(randutil.Int63n(100)) * time.Nanosecond)

				km.Unlock(key)
			}
		}("key-" + strconv.Itoa(i))
	}
	wg.Wait()
}

func TestMultiAcquireOnSameKey(t *testing.T) {
	t.Parallel()

	km := newKeyMutex()
	key := "c1"
	ctx := context.Background()

	assert.Nil(t, km.Lock(ctx, key))

	nproc := runtime.GOMAXPROCS(0)
	nloops := 10000

	var wg sync.WaitGroup
	for i := 0; i < nproc; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := 0; i < nloops; i++ {
				km.Lock(ctx, key)

				time.Sleep(time.Duration(randutil.Int63n(100)) * time.Nanosecond)

				km.Unlock(key)
			}
		}()
	}
	km.Unlock(key)
	wg.Wait()

	// c1 key has been released so the it should not have any klock.
	assert.Equal(t, len(km.locks), 0)
}
