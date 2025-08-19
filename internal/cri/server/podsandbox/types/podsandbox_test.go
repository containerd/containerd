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

package types

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

func Test_PodSandbox(t *testing.T) {
	p := NewPodSandbox("test", sandbox.Status{State: sandbox.StateUnknown})
	assert.Equal(t, sandbox.StateUnknown, p.Status.Get().State)
	assert.Equal(t, "test", p.ID)
	p.Metadata = sandbox.Metadata{ID: "test", NetNSPath: "/test"}
	createAt := time.Now()
	require.NoError(t, p.Status.Update(func(status sandbox.Status) (sandbox.Status, error) {
		status.State = sandbox.StateReady
		status.Pid = uint32(100)
		status.CreatedAt = createAt
		return status, nil
	}))
	status := p.Status.Get()
	assert.Equal(t, sandbox.StateReady, status.State)
	assert.Equal(t, uint32(100), status.Pid)
	assert.Equal(t, status.CreatedAt, createAt)

	exitAt := time.Now().Add(time.Second)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*500)
		defer cancel()
		_, err := p.Wait(ctx)
		assert.Equal(t, err, ctx.Err())
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		exitStatus, err := p.Wait(context.Background())
		require.NoError(t, err)
		code, exitTime, err := exitStatus.Result()
		require.NoError(t, err)
		assert.Equal(t, uint32(128), code)
		assert.Equal(t, exitTime, exitAt)
	}()
	time.Sleep(time.Second)
	if err := p.Exit(uint32(128), exitAt); err != nil {
		t.Fatalf("failed to set exit of pod sandbox %v", err)
	}
	wg.Wait()
}
