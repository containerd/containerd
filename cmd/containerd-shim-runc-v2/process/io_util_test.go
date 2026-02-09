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

package process

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertArgPair checks that a key-value pair exists in the command args.
// Args are expected as consecutive elements: [cmd, key, value, key, value, ...].
func assertArgPair(t *testing.T, args []string, key, value string) {
	t.Helper()
	for i := 1; i < len(args)-1; i++ {
		if args[i] == key {
			assert.Equal(t, value, args[i+1], "arg value mismatch for key %q", key)
			return
		}
	}
	t.Errorf("expected arg key %q not found in args %v", key, args)
}

func TestNewBinaryCmd(t *testing.T) {
	const (
		testID = "test-id"
		testNS = "test-ns"
	)

	testCases := []struct {
		name        string
		uri         string
		wantArgs    []string
		wantEnvs    []string
		wantEnvsNot []string
	}{
		{
			name:     "args only",
			uri:      "binary:///bin/logger?id=container1",
			wantArgs: []string{"id", "container1"},
		},
		{
			name:     "envs only",
			uri:      "binary:///bin/logger?env.LOG_LEVEL=debug&env.API_KEY=secret",
			wantArgs: []string{},
			wantEnvs: []string{"LOG_LEVEL=debug", "API_KEY=secret"},
		},
		{
			name:     "mixed args and envs",
			uri:      "binary:///bin/logger?id=container1&env.LOG_LEVEL=debug",
			wantArgs: []string{"id", "container1"},
			wantEnvs: []string{"LOG_LEVEL=debug"},
		},
		{
			name:        "cannot override CONTAINER_ID",
			uri:         "binary:///bin/logger?env.CONTAINER_ID=custom-id",
			wantArgs:    []string{},
			wantEnvsNot: []string{"CONTAINER_ID=custom-id"},
		},
		{
			name:        "cannot override CONTAINER_NAMESPACE",
			uri:         "binary:///bin/logger?env.CONTAINER_NAMESPACE=custom-ns",
			wantArgs:    []string{},
			wantEnvsNot: []string{"CONTAINER_NAMESPACE=custom-ns"},
		},
		{
			name:     "no query params",
			uri:      "binary:///bin/logger",
			wantArgs: []string{},
		},
		{
			name:     "empty env value",
			uri:      "binary:///bin/logger?env.EMPTY_VAR=",
			wantArgs: []string{},
			wantEnvs: []string{"EMPTY_VAR="},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := url.Parse(tc.uri)
			require.NoError(t, err)

			cmd := NewBinaryCmd(parsed, testID, testNS)

			assert.Equal(t, "/bin/logger", cmd.Path)

			// Check args (order may vary due to map iteration).
			for i := 0; i < len(tc.wantArgs); i += 2 {
				assertArgPair(t, cmd.Args, tc.wantArgs[i], tc.wantArgs[i+1])
			}

			// CONTAINER_ID and CONTAINER_NAMESPACE are always set from the provided id/ns.
			assert.Contains(t, cmd.Env, "CONTAINER_ID="+testID)
			assert.Contains(t, cmd.Env, "CONTAINER_NAMESPACE="+testNS)

			// Check expected envs are present.
			for _, env := range tc.wantEnvs {
				assert.Contains(t, cmd.Env, env, "expected env %q not found", env)
			}

			// Check unwanted envs are not present.
			for _, env := range tc.wantEnvsNot {
				assert.NotContains(t, cmd.Env, env, "unexpected env %q found", env)
			}
		})
	}
}
