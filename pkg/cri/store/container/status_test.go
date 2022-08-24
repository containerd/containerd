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

package container

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	assertlib "github.com/stretchr/testify/assert"
	requirelib "github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestContainerState(t *testing.T) {
	for c, test := range map[string]struct {
		status Status
		state  runtime.ContainerState
	}{
		"unknown state": {
			status: Status{
				Unknown: true,
			},
			state: runtime.ContainerState_CONTAINER_UNKNOWN,
		},
		"unknown state because there is no timestamp set": {
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
		t.Run(c, func(t *testing.T) {
			assertlib.Equal(t, test.state, test.status.State())
		})
	}
}

func TestStatusEncodeDecode(t *testing.T) {
	s := &Status{
		Pid:        1234,
		CreatedAt:  time.Now().UnixNano(),
		StartedAt:  time.Now().UnixNano(),
		FinishedAt: time.Now().UnixNano(),
		ExitCode:   1,
		Reason:     "test-reason",
		Message:    "test-message",
		Removing:   true,
		Starting:   true,
		Unknown:    true,
	}
	assert := assertlib.New(t)
	data, err := s.encode()
	assert.NoError(err)
	newS := &Status{}
	assert.NoError(newS.decode(data))
	s.Removing = false // Removing should not be encoded.
	s.Starting = false // Starting should not be encoded.
	s.Unknown = false  // Unknown should not be encoded.
	assert.Equal(s, newS)

	unsupported, err := json.Marshal(&versionedStatus{
		Version: "random-test-version",
		Status:  *s,
	})
	assert.NoError(err)
	assert.Error(newS.decode(unsupported))
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
	require := requirelib.New(t)

	tempDir := t.TempDir()
	statusFile := filepath.Join(tempDir, "status")

	t.Logf("simple store and get")
	s, err := StoreStatus(tempDir, testID, testStatus)
	assert.NoError(err)
	old := s.Get()
	assert.Equal(testStatus, old)
	_, err = os.Stat(statusFile)
	assert.NoError(err)
	loaded, err := LoadStatus(tempDir, testID)
	require.NoError(err)
	assert.Equal(testStatus, loaded)

	t.Logf("failed update should not take effect")
	err = s.Update(func(o Status) (Status, error) {
		return updateStatus, updateErr
	})
	assert.Equal(updateErr, err)
	assert.Equal(testStatus, s.Get())
	loaded, err = LoadStatus(tempDir, testID)
	require.NoError(err)
	assert.Equal(testStatus, loaded)

	t.Logf("successful update should take effect but not checkpoint")
	err = s.Update(func(o Status) (Status, error) {
		return updateStatus, nil
	})
	assert.NoError(err)
	assert.Equal(updateStatus, s.Get())
	loaded, err = LoadStatus(tempDir, testID)
	require.NoError(err)
	assert.Equal(testStatus, loaded)
	// Recover status.
	assert.NoError(s.Update(func(o Status) (Status, error) {
		return testStatus, nil
	}))

	t.Logf("failed update sync should not take effect")
	err = s.UpdateSync(func(o Status) (Status, error) {
		return updateStatus, updateErr
	})
	assert.Equal(updateErr, err)
	assert.Equal(testStatus, s.Get())
	loaded, err = LoadStatus(tempDir, testID)
	require.NoError(err)
	assert.Equal(testStatus, loaded)

	t.Logf("successful update sync should take effect and checkpoint")
	err = s.UpdateSync(func(o Status) (Status, error) {
		return updateStatus, nil
	})
	assert.NoError(err)
	assert.Equal(updateStatus, s.Get())
	loaded, err = LoadStatus(tempDir, testID)
	require.NoError(err)
	assert.Equal(updateStatus, loaded)

	t.Logf("successful update should not affect existing snapshot")
	assert.Equal(testStatus, old)

	t.Logf("delete status")
	assert.NoError(s.Delete())
	_, err = LoadStatus(tempDir, testID)
	assert.Error(err)
	_, err = os.Stat(statusFile)
	assert.True(os.IsNotExist(err))

	t.Logf("delete status should be idempotent")
	assert.NoError(s.Delete())
}
