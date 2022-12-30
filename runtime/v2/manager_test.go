//go:build !windows && !darwin

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
	"os"
	"testing"
)

// setupAbsoluteShimPath creates a temporary directory in $PATH with an empty
// shim executable file in it to test the exec.LookPath branch of resolveRuntimePath
func setupAbsoluteShimPath(t *testing.T) (string, error) {
	tempShimDir := t.TempDir()

	_, err := os.Create(tempShimDir + "/containerd-shim-runc-v2")
	if err != nil {
		return "", err
	}

	t.Setenv("PATH", tempShimDir+":"+os.Getenv("PATH"))
	absoluteShimPath := tempShimDir + "/containerd-shim-runc-v2"

	err = os.Chmod(absoluteShimPath, 0777)
	if err != nil {
		return "", err
	}

	return absoluteShimPath, nil
}

func TestResolveRuntimePath(t *testing.T) {
	sm := &ShimManager{}
	absoluteShimPath, err := setupAbsoluteShimPath(t)
	if err != nil {
		t.Errorf("Failed to create temporary shim path: %q", err)
	}

	tests := []struct {
		runtime string
		want    string
	}{
		{ // Absolute path
			runtime: absoluteShimPath,
			want:    absoluteShimPath,
		},
		{ // Binary name
			runtime: "io.containerd.runc.v2",
			want:    absoluteShimPath,
		},
		{ // Invalid absolute path
			runtime: "/fake/abs/path",
			want:    "",
		},
		{ // No name
			runtime: "",
			want:    "",
		},
		{ // Relative Path
			runtime: "./containerd-shim-runc-v2",
			want:    "",
		},
		{
			runtime: "fake/containerd-shim-runc-v2",
			want:    "",
		},
		{
			runtime: "./fake/containerd-shim-runc-v2",
			want:    "",
		},
		{ // Relative Path or Bad Binary Name
			runtime: ".io.containerd.runc.v2",
			want:    "",
		},
	}

	for _, c := range tests {
		have, _ := sm.resolveRuntimePath(c.runtime)
		if have != c.want {
			t.Errorf("Expected %q, got %q", c.want, have)
		}
	}
}
