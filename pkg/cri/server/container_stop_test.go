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

	containerstore "github.com/containerd/containerd/v2/pkg/cri/store/container"
)

func TestWaitContainerStop(t *testing.T) {
	id := "test-id"
	for _, test := range []struct {
		desc      string
		status    *containerstore.Status
		cancel    bool
		timeout   time.Duration
		expectErr bool
	}{
		{
			desc: "should return error if timeout exceeds",
			status: &containerstore.Status{
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			timeout:   200 * time.Millisecond,
			expectErr: true,
		},
		{
			desc: "should return error if context is cancelled",
			status: &containerstore.Status{
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			timeout:   time.Hour,
			cancel:    true,
			expectErr: true,
		},
		{
			desc: "should not return error if container is stopped before timeout",
			status: &containerstore.Status{
				CreatedAt:  time.Now().UnixNano(),
				StartedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
			},
			timeout:   time.Hour,
			expectErr: false,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			c := newTestCRIService()
			container, err := containerstore.NewContainer(
				containerstore.Metadata{ID: id},
				containerstore.WithFakeStatus(*test.status),
			)
			assert.NoError(t, err)
			assert.NoError(t, c.containerStore.Add(container))
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
			err = c.waitContainerStop(ctx, container)
			assert.Equal(t, test.expectErr, err != nil, test.desc)
		})
	}
}
