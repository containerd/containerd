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

package client

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration(t *testing.T) {
	currentDefault := filepath.Join(t.TempDir(), "default.toml")
	defaultContent, err := currentDefaultConfig()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(currentDefault, []byte(defaultContent), 0644))

	type migrationTest struct {
		Name     string
		File     string
		Migrated string
	}
	migrationTests := []migrationTest{
		{
			Name:     "CurrentDefault",
			File:     currentDefault,
			Migrated: defaultContent,
		},
	}

	// Only run the old version migration tests for the same platform
	// and build settings the default config was generated for.
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" && strings.Contains(defaultContent, "btrfs") && strings.Contains(defaultContent, "devmapper") {
		migrationTests = append(migrationTests, []migrationTest{
			{
				Name:     "1.6-Default",
				File:     "testdata/default-1.6.toml",
				Migrated: defaultContent,
			},
			{
				Name:     "1.7-Default",
				File:     "testdata/default-1.7.toml",
				Migrated: defaultContent,
			},
		}...)
	}

	for _, tc := range migrationTests {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			buf := bytes.NewBuffer(nil)
			cmd := exec.Command("containerd", "-c", tc.File, "config", "migrate")
			cmd.Stdout = buf
			cmd.Stderr = os.Stderr
			require.NoError(t, cmd.Run())
			actual := buf.String()
			assert.Equal(t, tc.Migrated, actual)
		})
	}

}

func currentDefaultConfig() (string, error) {
	cmd := exec.Command("containerd", "config", "default")
	buf := bytes.NewBuffer(nil)
	cmd.Stdout = buf
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return buf.String(), nil
}
