/*
Copyright 2017 The Kubernetes Authors.

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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"

	containerstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/container"
)

func TestWaitContainerStop(t *testing.T) {
	id := "test-id"
	for desc, test := range map[string]struct {
		status    *containerstore.Status
		cancel    bool
		timeout   time.Duration
		expectErr bool
	}{
		"should return error if timeout exceeds": {
			status: &containerstore.Status{
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			timeout:   2 * stopCheckPollInterval,
			expectErr: true,
		},
		"should return error if context is cancelled": {
			status: &containerstore.Status{
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			timeout:   time.Hour,
			cancel:    true,
			expectErr: true,
		},
		"should not return error if container is removed before timeout": {
			status:    nil,
			timeout:   time.Hour,
			expectErr: false,
		},
		"should not return error if container is stopped before timeout": {
			status: &containerstore.Status{
				CreatedAt:  time.Now().UnixNano(),
				StartedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
			},
			timeout:   time.Hour,
			expectErr: false,
		},
	} {
		c := newTestCRIContainerdService()
		if test.status != nil {
			container, err := containerstore.NewContainer(
				containerstore.Metadata{ID: id},
				containerstore.WithFakeStatus(*test.status),
			)
			assert.NoError(t, err)
			assert.NoError(t, c.containerStore.Add(container))
		}
		ctx := context.Background()
		if test.cancel {
			cancelledCtx, cancel := context.WithCancel(ctx)
			cancel()
			ctx = cancelledCtx
		}
		err := c.waitContainerStop(ctx, id, test.timeout)
		assert.Equal(t, test.expectErr, err != nil, desc)
	}
}
