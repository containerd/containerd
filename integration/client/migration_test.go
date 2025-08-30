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

	traceAPI "github.com/containerd/containerd/v2/pkg/tracing"
	traceManager "github.com/containerd/containerd/v2/pkg/tracing/manager"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	traceAPI.SetGlobalTraceManager(traceManager.NewNoopManager())
}

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

	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" && strings.Contains(defaultContent, "btrfs") && strings.Contains(defaultContent, "devmapper") {
		migrationTests = append(migrationTests, []migrationTest{
			{
				Name:     "1.6-Default",
				File:     "testdata/default-1.6.toml",
				Migrated: patchExpectedConfig(defaultContent, map[string]string{}, []string{}),
			},
			{
				Name:     "1.7-Default",
				File:     "testdata/default-1.7.toml",
				Migrated: patchExpectedConfig(defaultContent, map[string]string{}, []string{}),
			},
			{
				Name: "1.7-Custom",
				File: "testdata/custom-1.7.toml",
				Migrated: patchExpectedConfig(defaultContent, map[string]string{
					"sandbox":               "'custom.io/pause:3.10.1'",
					"stream_idle_timeout":   "'2h0m0s'",
					"stream_server_address": "'127.0.1.1'",
					"stream_server_port":    "'15000'",
					"enable_tls_streaming":  "true",
				}, []string{}),
			},
		}...)
	}

	for _, tc := range migrationTests {
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

func patchExpectedConfig(src string, values map[string]string, extraBlocks []string) string {
	lines := strings.Split(src, "\n")

	for i, line := range lines {
		if strings.Contains(strings.TrimSpace(line), "disabled_plugins =") {
			lines[i] = "disabled_plugins = []"
			break
		}
	}

	// for i, line := range lines {
	// 	if strings.Contains(strings.TrimSpace(line), "[plugins.'io.containerd.internal.v1.enhanced_tracing']") {
	// 		hasExporters := false
	// 		for j := i + 1; j < len(lines); j++ {
	// 			trimmed := strings.TrimSpace(lines[j])
	// 			if strings.HasPrefix(trimmed, "[") {
	// 				break
	// 			}
	// 			if strings.Contains(trimmed, "exporters =") {
	// 				hasExporters = true
	// 				break
	// 			}
	// 		}

	// 		if !hasExporters {
	// 			insertIndex := -1
	// 			for j := i + 1; j < len(lines); j++ {
	// 				trimmed := strings.TrimSpace(lines[j])
	// 				if strings.HasPrefix(trimmed, "[") {
	// 					insertIndex = j
	// 					break
	// 				}
	// 				if trimmed == "" {
	// 					insertIndex = j
	// 					break
	// 				}
	// 			}

	// 			if insertIndex == -1 {
	// 				insertIndex = i + 1
	// 			}

	// 			newLines := make([]string, 0, len(lines)+1)
	// 			newLines = append(newLines, lines[:insertIndex]...)
	// 			newLines = append(newLines, "    exporters = ['noop']")
	// 			newLines = append(newLines, lines[insertIndex:]...)
	// 			lines = newLines
	// 		}
	// 		break
	// 	}
	// }

	var filteredLines []string
	skipBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "[") {
			skipBlock = false
		}

		if !skipBlock {
			filteredLines = append(filteredLines, line)
		}
	}
	lines = filteredLines

	for i, line := range lines {
		for k, v := range values {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, k+" =") {
				indent := strings.Repeat(" ", len(line)-len(strings.TrimLeft(line, " ")))
				lines[i] = indent + k + " = " + v
			}
		}
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "use_internal_loopback = false" && i > 0 {
			prevLine := strings.TrimSpace(lines[i-1])
			if strings.HasPrefix(prevLine, "ip_pref =") {
				indent := strings.Repeat(" ", len(lines[i-1])-len(strings.TrimLeft(lines[i-1], " ")))
				lines[i] = indent + "use_internal_loopback = false"
			}
		}
	}

	if len(extraBlocks) > 0 {
		insertIndex := -1
		for i := len(lines) - 1; i >= 0; i-- {
			if strings.Contains(lines[i], "[plugins.'io.containerd.internal.v1.opt']") {
				insertIndex = i
				break
			}
		}

		if insertIndex > 0 {
			newLines := make([]string, 0, len(lines)+len(extraBlocks))
			newLines = append(newLines, lines[:insertIndex]...)
			newLines = append(newLines, extraBlocks...)
			newLines = append(newLines, lines[insertIndex:]...)
			lines = newLines
		}
	}

	return strings.Join(lines, "\n")
}