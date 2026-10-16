//go:build linux

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

package util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveCriuPath(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "criu-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create a dummy executable criu
	execDir := filepath.Join(tempDir, "bin-exec")
	if err := os.MkdirAll(execDir, 0755); err != nil {
		t.Fatal(err)
	}
	execPath := filepath.Join(execDir, "criu")
	if err := os.WriteFile(execPath, []byte("dummy"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create a dummy non-executable criu
	nonExecDir := filepath.Join(tempDir, "bin-nonexec")
	if err := os.MkdirAll(nonExecDir, 0755); err != nil {
		t.Fatal(err)
	}
	nonExecPath := filepath.Join(nonExecDir, "criu")
	if err := os.WriteFile(nonExecPath, []byte("dummy"), 0644); err != nil {
		t.Fatal(err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relDir, err := filepath.Rel(wd, execDir)
	if err != nil {
		t.Fatal(err)
	}

	// Mock the system PATH to point to our executable directory
	t.Setenv("PATH", execDir)

	tests := []struct {
		name         string
		customPath   string
		expectedPath string
	}{
		{
			name:         "custom PATH with executable criu",
			customPath:   execDir,
			expectedPath: execPath,
		},
		{
			name:         "custom PATH with non-executable criu",
			customPath:   nonExecDir,
			expectedPath: "",
		},
		{
			name:         "multiple directories in PATH, executable in second",
			customPath:   nonExecDir + string(filepath.ListSeparator) + execDir,
			expectedPath: execPath,
		},
		{
			name:         "custom PATH with relative directory is skipped",
			customPath:   relDir,
			expectedPath: "",
		},
		{
			name:         "empty customPath falls back to system PATH",
			customPath:   "",
			expectedPath: execPath, // Falls back to system PATH (mocked to execDir)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := resolveCriuPath(tt.customPath)
			if path != tt.expectedPath {
				t.Errorf("expected %s, got %s", tt.expectedPath, path)
			}
		})
	}
}
