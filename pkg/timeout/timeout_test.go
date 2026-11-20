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

package timeout

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cleanupKeys removes keys from the package-global timeouts map so that
// repeated test invocations (e.g. -count=2) start from the unset-key state.
func cleanupKeys(t *testing.T, keys ...string) {
	t.Cleanup(func() {
		mu.Lock()
		for _, key := range keys {
			delete(timeouts, key)
		}
		mu.Unlock()
	})
}

func TestGet(t *testing.T) {
	const key = "test.get"
	cleanupKeys(t, key)

	assert.Equal(t, DefaultTimeout, Get(key), "unset key should fall back to DefaultTimeout")

	Set(key, 3*time.Second)
	assert.Equal(t, 3*time.Second, Get(key))

	Set(key, 6*time.Second)
	assert.Equal(t, 6*time.Second, Get(key), "Set should overwrite an existing timeout")
}

func TestWithContext(t *testing.T) {
	const key = "test.withcontext"
	cleanupKeys(t, key)
	d := 30 * time.Second
	Set(key, d)

	start := time.Now()
	ctx, cancel := WithContext(context.Background(), key)
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok, "context should have a deadline")
	assert.False(t, deadline.Before(start.Add(d)), "deadline should not be earlier than start+%v", d)
	assert.False(t, deadline.After(start.Add(d+5*time.Second)), "deadline should not be later than start+%v", d+5*time.Second)
}

func TestAll(t *testing.T) {
	const (
		keyA = "test.all.a"
		keyB = "test.all.b"
	)
	cleanupKeys(t, keyA, keyB)
	Set(keyA, time.Second)
	Set(keyB, time.Minute)

	all := All()
	assert.Equal(t, time.Second, all[keyA])
	assert.Equal(t, time.Minute, all[keyB])

	// All returns a copy: mutations must not leak into the package state.
	all[keyA] = time.Hour
	delete(all, keyB)
	assert.Equal(t, time.Second, Get(keyA))
	assert.Equal(t, time.Minute, Get(keyB))
}
