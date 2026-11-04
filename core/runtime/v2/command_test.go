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

func TestCommandGOMAXPROCS(t *testing.T) {
	for _, tc := range []struct {
		name      string
		inherited string // GOMAXPROCS in containerd's environment, if set
		unset     bool
		shimEnv   []string
		expected  []string
	}{
		{name: "unset", unset: true},
		{name: "inherited", inherited: "1", expected: []string{"1"}},
		{name: "shim env", unset: true, shimEnv: []string{"GOMAXPROCS=3"}, expected: []string{"3"}},
		{name: "shim env overrides inherited", inherited: "1", shimEnv: []string{"GOMAXPROCS=3"}, expected: []string{"3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOMAXPROCS", tc.inherited)
			if tc.unset {
				require.NoError(t, os.Unsetenv("GOMAXPROCS"))
			}
			ctx := namespaces.WithNamespace(context.Background(), "test-ns")
			cmd, err := command(ctx, &commandConfig{
				ID:          "test-shim",
				RuntimePath: "/usr/bin/containerd-shim-runc-v2",
				Env:         tc.shimEnv,
				Action:      "delete",
			})
			require.NoError(t, err)
			// Environ applies exec.Cmd's duplicate-variable handling.
			var values []string
			for _, entry := range cmd.Environ() {
				if value, ok := strings.CutPrefix(entry, "GOMAXPROCS="); ok {
					values = append(values, value)
				}
			}
			require.Equal(t, tc.expected, values)
		})
	}
}
