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

package shimenv

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandAndDaemonEnv(t *testing.T) {
	for _, tc := range []struct {
		name             string
		daemonEnv        []string
		shimEnv          []string
		wantStartGOMAX   []string
		wantDeleteGOMAX  []string
		wantLongRunGOMAX []string
		wantGOGC         []string
	}{
		{
			name:             "defaults when nothing configured",
			daemonEnv:        []string{"PATH=/usr/bin"},
			wantStartGOMAX:   []string{"2"},
			wantDeleteGOMAX:  []string{"2"},
			wantLongRunGOMAX: []string{"4"},
		},
		{
			name:             "daemon GOMAXPROCS and GOGC isolated from shims",
			daemonEnv:        []string{"PATH=/usr/bin", "GOMAXPROCS=16", "GOGC=200", "GOMEMLIMIT=32MiB"},
			wantStartGOMAX:   []string{"2"},
			wantDeleteGOMAX:  []string{"2"},
			wantLongRunGOMAX: []string{"4"},
		},
		{
			name:             "shim env GOMAXPROCS=1 and GOGC=30 override defaults and daemon env",
			daemonEnv:        []string{"GOMAXPROCS=16", "GOGC=200"},
			shimEnv:          []string{"GOMAXPROCS=1", "GOGC=30"},
			wantStartGOMAX:   []string{"1"},
			wantDeleteGOMAX:  []string{"1"},
			wantLongRunGOMAX: []string{"1"},
			wantGOGC:         []string{"30"},
		},
		{
			name:             "explicit shim env GOMAXPROCS=2 preserved in long-running daemon",
			daemonEnv:        []string{"GOMAXPROCS=16"},
			shimEnv:          []string{"GOMAXPROCS=2"},
			wantStartGOMAX:   []string{"2"},
			wantDeleteGOMAX:  []string{"2"},
			wantLongRunGOMAX: []string{"2"},
		},
		{
			name:             "empty GOMAXPROCS in shim env falls back to defaults",
			daemonEnv:        []string{"GOMAXPROCS=16"},
			shimEnv:          []string{"GOMAXPROCS=1", "GOMAXPROCS="},
			wantStartGOMAX:   []string{"1", "", "2"},
			wantDeleteGOMAX:  []string{"1", "", "2"},
			wantLongRunGOMAX: []string{"4"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			startEnv := CommandEnv(tc.daemonEnv, []string{"NAMESPACE=k8s.io"}, tc.shimEnv, true)
			deleteEnv := CommandEnv(tc.daemonEnv, []string{"NAMESPACE=k8s.io"}, tc.shimEnv, false)
			longRunEnv := DaemonEnv(startEnv, "4")

			require.Equal(t, tc.wantStartGOMAX, filterPrefix(startEnv, "GOMAXPROCS="))
			require.Equal(t, tc.wantDeleteGOMAX, filterPrefix(deleteEnv, "GOMAXPROCS="))
			require.Equal(t, tc.wantLongRunGOMAX, filterPrefix(longRunEnv, "GOMAXPROCS="))
			require.Equal(t, tc.wantGOGC, filterPrefix(startEnv, "GOGC="))
			require.Equal(t, tc.wantGOGC, filterPrefix(deleteEnv, "GOGC="))
			require.Equal(t, tc.wantGOGC, filterPrefix(longRunEnv, "GOGC="))

			// Internal marker must never appear in delete or in the long-running shim daemon.
			require.Empty(t, filterPrefix(deleteEnv, explicitGOMAXPROCSEnv+"="))
			require.Empty(t, filterPrefix(longRunEnv, explicitGOMAXPROCSEnv+"="))
		})
	}
}

func filterPrefix(env []string, prefix string) []string {
	var out []string
	for _, entry := range env {
		if v, ok := strings.CutPrefix(entry, prefix); ok {
			out = append(out, v)
		}
	}
	return out
}
