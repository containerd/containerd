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
	"context"
	"os"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/stretchr/testify/require"
)

func TestCommandRuntimeEnv(t *testing.T) {
	for _, tc := range []struct {
		name               string
		daemonGOMAXPROCS   string
		unsetGOMAXPROCS    bool
		daemonGOGC         string
		unsetGOGC          bool
		shimEnv            []string
		expectedGOMAXPROCS []string
		expectedGOGC       []string
	}{
		{
			name:               "defaults when unset",
			unsetGOMAXPROCS:    true,
			unsetGOGC:          true,
			expectedGOMAXPROCS: []string{"2"},
		},
		{
			name:               "daemon GOMAXPROCS and GOGC do not bleed into shims",
			daemonGOMAXPROCS:   "16",
			daemonGOGC:         "200",
			expectedGOMAXPROCS: []string{"2"},
		},
		{
			name:               "shim manager env sets GOMAXPROCS and GOGC",
			unsetGOMAXPROCS:    true,
			unsetGOGC:          true,
			shimEnv:            []string{"GOMAXPROCS=1", "GOGC=30"},
			expectedGOMAXPROCS: []string{"1"},
			expectedGOGC:       []string{"30"},
		},
		{
			name:               "shim manager env overrides while daemon env is ignored",
			daemonGOMAXPROCS:   "16",
			daemonGOGC:         "200",
			shimEnv:            []string{"GOMAXPROCS=3", "GOGC=50"},
			expectedGOMAXPROCS: []string{"3"},
			expectedGOGC:       []string{"50"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOMAXPROCS", tc.daemonGOMAXPROCS)
			if tc.unsetGOMAXPROCS {
				require.NoError(t, os.Unsetenv("GOMAXPROCS"))
			}
			t.Setenv("GOGC", tc.daemonGOGC)
			if tc.unsetGOGC {
				require.NoError(t, os.Unsetenv("GOGC"))
			}
			ctx := namespaces.WithNamespace(context.Background(), "test-ns")
			for _, action := range []string{"start", "delete"} {
				cmd, err := command(ctx, &commandConfig{
					ID:          "test-shim",
					RuntimePath: "/usr/bin/containerd-shim-runc-v2",
					Env:         tc.shimEnv,
					Action:      action,
				})
				require.NoError(t, err)
				// Environ applies exec.Cmd's duplicate-variable handling.
				var gomaxprocs, gogc []string
				for _, entry := range cmd.Environ() {
					if value, ok := strings.CutPrefix(entry, "GOMAXPROCS="); ok {
						gomaxprocs = append(gomaxprocs, value)
					}
					if value, ok := strings.CutPrefix(entry, "GOGC="); ok {
						gogc = append(gogc, value)
					}
				}
				require.Equal(t, tc.expectedGOMAXPROCS, gomaxprocs, "action=%s", action)
				require.Equal(t, tc.expectedGOGC, gogc, "action=%s", action)
			}
		})
	}
}
