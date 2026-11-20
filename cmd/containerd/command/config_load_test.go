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

package command

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestConfigCommandMissingFiles(t *testing.T) {
	dir := t.TempDir()
	mainFile := filepath.Join(dir, "main.toml")
	nestedFile := filepath.Join(dir, "nested.toml")
	missing := filepath.Join(dir, "missing.toml")
	globFile := filepath.Join(dir, "glob.toml")
	require.NoError(t, os.WriteFile(mainFile, []byte("version = 4\nimports = ['nested.toml']\n"), 0600))
	require.NoError(t, os.WriteFile(nestedFile, []byte("version = 4\nimports = ['missing.toml']\n"), 0600))
	require.NoError(t, os.WriteFile(globFile, []byte("version = 4\nimports = ['absent-*.toml']\n"), 0600))

	for _, subcommand := range []string{"dump", "migrate"} {
		for _, tc := range []struct {
			name         string
			defaultPath  string
			explicitPath string
			wantMissing  bool
		}{
			{name: "absent default", defaultPath: missing},
			{name: "absent explicit file", explicitPath: missing, wantMissing: true},
			{name: "missing import in default file", defaultPath: mainFile, wantMissing: true},
			{name: "missing import in explicit file", explicitPath: mainFile, wantMissing: true},
			{name: "unmatched import glob", explicitPath: globFile},
		} {
			t.Run(subcommand+"/"+tc.name, func(t *testing.T) {
				app := App()
				for _, f := range app.Flags {
					if flag, ok := f.(*cli.StringFlag); ok && flag.Name == "config" {
						flag.Value = tc.defaultPath
					}
				}
				args := []string{"containerd"}
				if tc.explicitPath != "" {
					args = append(args, "--config", tc.explicitPath)
				}
				err := app.Run(t.Context(), append(args, "config", subcommand))
				if tc.wantMissing {
					require.ErrorIs(t, err, os.ErrNotExist)
					var pathErr *os.PathError
					require.ErrorAs(t, err, &pathErr)
					require.Equal(t, missing, pathErr.Path)
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
}
