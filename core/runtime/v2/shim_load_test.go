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

package v2

import (
	"errors"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/require"

	runtimeapi "github.com/containerd/containerd/v2/core/runtime"
)

func TestShouldCleanupShim(t *testing.T) {
	otherErr := errors.New("some other error")

	testCases := []struct {
		Name     string
		SgetErr  error
		PidErr   error
		PInfo    []runtimeapi.ProcessInfo
		Expected bool
	}{
		{
			Name:     "sandbox found",
			SgetErr:  nil,
			PidErr:   nil,
			PInfo:    nil,
			Expected: false,
		},
		{
			Name:     "sandbox lookup fails with unrelated error",
			SgetErr:  otherErr,
			PidErr:   nil,
			PInfo:    nil,
			Expected: false,
		},
		{
			Name:     "not a sandbox, no pids running",
			SgetErr:  errdefs.ErrNotFound,
			PidErr:   nil,
			PInfo:    []runtimeapi.ProcessInfo{},
			Expected: true,
		},
		{
			Name:     "not a sandbox, pids still running",
			SgetErr:  errdefs.ErrNotFound,
			PidErr:   nil,
			PInfo:    []runtimeapi.ProcessInfo{{Pid: 1234}},
			Expected: false,
		},
		{
			Name:     "not a sandbox, pids lookup returns not found",
			SgetErr:  errdefs.ErrNotFound,
			PidErr:   errdefs.ErrNotFound,
			PInfo:    nil,
			Expected: true,
		},
		{
			Name:     "not a sandbox, pids lookup fails with other error",
			SgetErr:  errdefs.ErrNotFound,
			PidErr:   otherErr,
			PInfo:    nil,
			Expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			require.Equal(t, tc.Expected, shouldCleanupShim(tc.SgetErr, tc.PidErr, tc.PInfo))
		})
	}
}
