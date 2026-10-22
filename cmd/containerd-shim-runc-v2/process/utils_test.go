//go:build !windows

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

package process

import (
	"fmt"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"golang.org/x/sys/unix"
)

func TestCheckKillError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		expectNF    bool
		expectNil   bool
		description string
	}{
		{
			name:      "nil error",
			err:       nil,
			expectNil: true,
		},
		{
			name:     "process already finished",
			err:      fmt.Errorf("os: process already finished"),
			expectNF: true,
		},
		{
			name:     "container not running",
			err:      fmt.Errorf("container not running"),
			expectNF: true,
		},
		{
			name:     "no such process",
			err:      fmt.Errorf("no such process"),
			expectNF: true,
		},
		{
			name:     "ESRCH",
			err:      unix.ESRCH,
			expectNF: true,
		},
		{
			name:     "does not exist",
			err:      fmt.Errorf("container does not exist"),
			expectNF: true,
		},
		{
			name:        "no such file or directory from crun kill",
			err:         fmt.Errorf("crun: cannot open directory /run/crun/abc123: No such file or directory"),
			expectNF:    true,
			description: "crun returns this when state files at /run/crun/<id>/ are already removed by a prior delete",
		},
		{
			name:        "no such file or directory from runc kill",
			err:         fmt.Errorf("open /run/runc/k8s.io/abc123/state.json: no such file or directory"),
			expectNF:    true,
			description: "runc returns this when state files have been removed",
		},
		{
			name:     "other error propagates",
			err:      fmt.Errorf("connection refused"),
			expectNF: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checkKillError(tt.err)
			if tt.expectNil {
				assert.NoError(t, result)
			} else if tt.expectNF {
				assert.True(t, errdefs.IsNotFound(result),
					"expected ErrNotFound, got: %v", result)
			} else {
				assert.Error(t, result)
				assert.False(t, errdefs.IsNotFound(result),
					"expected non-NotFound error, got: %v", result)
			}
		})
	}
}
