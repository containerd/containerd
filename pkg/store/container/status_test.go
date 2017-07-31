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

package container

import (
	"errors"
	"testing"
	"time"

	assertlib "github.com/stretchr/testify/assert"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
)

func TestContainerState(t *testing.T) {
	for c, test := range map[string]struct {
		status Status
		state  runtime.ContainerState
	}{
		"unknown state": {
			status: Status{},
			state:  runtime.ContainerState_CONTAINER_UNKNOWN,
		},
		"created state": {
			status: Status{
				CreatedAt: time.Now().UnixNano(),
			},
			state: runtime.ContainerState_CONTAINER_CREATED,
		},
		"running state": {
			status: Status{
				CreatedAt: time.Now().UnixNano(),
				StartedAt: time.Now().UnixNano(),
			},
			state: runtime.ContainerState_CONTAINER_RUNNING,
		},
		"exited state": {
			status: Status{
				CreatedAt:  time.Now().UnixNano(),
				FinishedAt: time.Now().UnixNano(),
			},
			state: runtime.ContainerState_CONTAINER_EXITED,
		},
	} {
		t.Logf("TestCase %q", c)
		assertlib.Equal(t, test.state, test.status.State())
	}
}

func TestStatus(t *testing.T) {
	testID := "test-id"
	testStatus := Status{
		CreatedAt: time.Now().UnixNano(),
	}
	updateStatus := Status{
		CreatedAt: time.Now().UnixNano(),
		StartedAt: time.Now().UnixNano(),
	}
	updateErr := errors.New("update error")
	assert := assertlib.New(t)

	t.Logf("simple store and get")
	s, err := StoreStatus(testID, testStatus)
	assert.NoError(err)
	old := s.Get()
	assert.Equal(testStatus, old)

	t.Logf("failed update should not take effect")
	err = s.Update(func(o Status) (Status, error) {
		o = updateStatus
		return o, updateErr
	})
	assert.Equal(updateErr, err)
	assert.Equal(testStatus, s.Get())

	t.Logf("successful update should take effect")
	err = s.Update(func(o Status) (Status, error) {
		o = updateStatus
		return o, nil
	})
	assert.NoError(err)
	assert.Equal(updateStatus, s.Get())

	t.Logf("successful update should not affect existing snapshot")
	assert.Equal(testStatus, old)

	// TODO(random-liu): Test Load and Delete after disc checkpoint is added.
}
