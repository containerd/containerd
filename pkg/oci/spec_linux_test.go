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

package oci

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPossibleCPUs_HostSysfs(t *testing.T) {
	const possiblePath = "/sys/devices/system/cpu/possible"
	data, err := os.ReadFile(possiblePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Skipf("%s not present on host", possiblePath)
		}
		t.Fatalf("failed to read %s: %v", possiblePath, err)
	}

	content := strings.TrimSpace(string(data))
	parsed, err := parsePossibleCPUs(content)
	require.NoError(t, err, "host %s content %q failed to parse", possiblePath, content)
	require.NotEmpty(t, parsed, "parsed CPU list should not be empty")

	cachedParsed, err := possibleCPUsParsed()
	require.NoError(t, err, "possibleCPUsParsed() returned error")
	assert.Equal(t, parsed, cachedParsed, "possibleCPUsParsed() should match parsePossibleCPUs")

	cpus := possibleCPUs()
	assert.Equal(t, parsed, cpus, "possibleCPUs() should return parsed possible CPUs without falling back to NumCPU counting")
	assert.GreaterOrEqual(t, len(cpus), runtime.NumCPU(), "possible CPUs count should be >= runtime.NumCPU()")
}

func TestDefaultLinuxMaskedPaths(t *testing.T) {
	paths := defaultLinuxMaskedPaths()
	assert.Contains(t, paths, "/proc/interrupts")

	root := "/sys/devices/system/cpu"
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Skipf("%s not present on host", root)
		}
		t.Fatalf("failed to read %s: %v", root, err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "cpu") {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimPrefix(name, "cpu")); err != nil {
			continue
		}
		thermalPath := filepath.Join(root, name, "thermal_throttle")
		if _, err := os.Stat(thermalPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			t.Fatalf("unexpected error stating %s: %v", thermalPath, err)
		}
		assert.Contains(t, paths, thermalPath)
	}
}

func TestDefaultLinuxMaskedPaths_Clone(t *testing.T) {
	paths1 := defaultLinuxMaskedPaths()
	paths2 := defaultLinuxMaskedPaths()
	require.Equal(t, paths1, paths2)
	require.NotEmpty(t, paths1)

	paths1[0] = "/mutated"
	paths3 := defaultLinuxMaskedPaths()
	assert.NotEqual(t, paths1[0], paths3[0])
}
