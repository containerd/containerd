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

package sandbox

import (
	"errors"
	"testing"
	"time"

	assertlib "github.com/stretchr/testify/assert"
)

func TestStatus(t *testing.T) {
	testStatus := Status{
		Pid:       123,
		CreatedAt: time.Now(),
		State:     StateUnknown,
	}
	updateStatus := Status{
		Pid:       456,
		CreatedAt: time.Now(),
		State:     StateReady,
	}
	updateErr := errors.New("update error")
	assert := assertlib.New(t)

	t.Logf("simple store and get")
	s := StoreStatus(testStatus)
	old := s.Get()
	assert.Equal(testStatus, old)

	t.Logf("failed update should not take effect")
	err := s.Update(func(o Status) (Status, error) {
		return updateStatus, updateErr
	})
	assert.Equal(updateErr, err)
	assert.Equal(testStatus, s.Get())

	t.Logf("successful update should take effect but not checkpoint")
	err = s.Update(func(o Status) (Status, error) {
		return updateStatus, nil
	})
	assert.NoError(err)
	assert.Equal(updateStatus, s.Get())
}

func TestStateStringConversion(t *testing.T) {
	assert := assertlib.New(t)
	assert.Equal("SANDBOX_READY", StateReady.String())
	assert.Equal("SANDBOX_NOTREADY", StateNotReady.String())
	assert.Equal("SANDBOX_UNKNOWN", StateUnknown.String())
	assert.Equal("invalid sandbox state value: 123", State(123).String())
}
