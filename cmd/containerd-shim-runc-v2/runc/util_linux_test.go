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

package runc

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	runcC "github.com/containerd/go-runc"
)

func TestExitStatusFile(t *testing.T) {
	now := time.Now().Round(0).UTC()

	for _, tc := range []struct {
		name     string
		input    runcC.Exit
		corrupt  func(*testing.T, string)
		expected runcC.Exit
		hasErr   bool
	}{
		{
			name:   "invalid pid",
			input:  runcC.Exit{Pid: -1, Timestamp: time.Now(), Status: 1},
			hasErr: true,
		},
		{
			name:   "invalid status",
			input:  runcC.Exit{Pid: 10, Timestamp: time.Now(), Status: -1},
			hasErr: true,
		},
		{
			name:   "empty timestamp",
			input:  runcC.Exit{Pid: 10, Status: 0},
			hasErr: true,
		},
		{
			name:     "no error",
			input:    runcC.Exit{Pid: 10, Timestamp: now, Status: 0},
			expected: runcC.Exit{Pid: 10, Timestamp: now, Status: 0},
		},
		{
			name:  "file corrupted",
			input: runcC.Exit{Pid: 10, Timestamp: now, Status: 0},
			corrupt: func(t *testing.T, bundlePath string) {
				target := filepath.Join(bundlePath, exitStatusFileName)
				err := os.WriteFile(target, []byte("{oops:...}"), 0600)
				if err != nil {
					t.Fatalf("failed to corrupt file: %v", err)
				}
			},
			hasErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if err := WriteExitStatus(tmpDir, tc.input); err != nil {
				t.Fatalf("failed to write exit status: %v", err)
			}

			if tc.corrupt != nil {
				tc.corrupt(t, tmpDir)
			}

			got, err := ReadExitStatus(tmpDir)
			if tc.hasErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}
			if !reflect.DeepEqual(tc.expected, got) {
				t.Fatalf("expected %v, but got %v", tc.expected, got)
			}
		})
	}
}
