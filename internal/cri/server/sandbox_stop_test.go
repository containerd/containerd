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

package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

func TestWaitSandboxStop(t *testing.T) {
	id := "test-id"
	for _, test := range []struct {
		desc      string
		state     sandboxstore.State
		cancel    bool
		timeout   time.Duration
		expectErr bool
	}{
		{
			desc:      "should return error if timeout exceeds",
			state:     sandboxstore.StateReady,
			timeout:   200 * time.Millisecond,
			expectErr: true,
		},
		{
			desc:      "should return error if context is cancelled",
			state:     sandboxstore.StateReady,
			timeout:   time.Hour,
			cancel:    true,
			expectErr: true,
		},
		{
			desc:      "should not return error if sandbox is stopped before timeout",
			state:     sandboxstore.StateNotReady,
			timeout:   time.Hour,
			expectErr: false,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			c := newTestCRIService()
			sandbox := sandboxstore.NewSandbox(
				sandboxstore.Metadata{ID: id},
				sandboxstore.Status{State: test.state},
			)
			ctx := context.Background()
			if test.cancel {
				cancelledCtx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelledCtx
			}
			if test.timeout > 0 {
				timeoutCtx, cancel := context.WithTimeout(ctx, test.timeout)
				defer cancel()
				ctx = timeoutCtx
			}
			err := c.waitSandboxStop(ctx, sandbox)
			assert.Equal(t, test.expectErr, err != nil, test.desc)
		})
	}
}
