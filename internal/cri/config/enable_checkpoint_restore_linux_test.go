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

package config

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/pkg/deprecation"
)

func TestParseEnableCheckpointRestore(t *testing.T) {
	testCases := []struct {
		name     string
		tomlStr  string
		expected *bool
	}{
		{
			name: "enable_checkpoint_restore set to true",
			tomlStr: `
enable_checkpoint_restore = true
`,
			expected: func() *bool { v := true; return &v }(),
		},
		{
			name: "enable_checkpoint_restore set to false",
			tomlStr: `
enable_checkpoint_restore = false
`,
			expected: func() *bool { v := false; return &v }(),
		},
		{
			name: "enable_checkpoint_restore absent",
			tomlStr: `
# empty or other fields
`,
			expected: func() *bool { v := true; return &v }(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultRuntimeConfig()
			err := toml.Unmarshal([]byte(tc.tomlStr), &cfg)
			assert.NoError(t, err)
			if tc.expected == nil {
				assert.Nil(t, cfg.EnableCheckpointRestore)
			} else {
				if assert.NotNil(t, cfg.EnableCheckpointRestore) {
					assert.Equal(t, *tc.expected, *cfg.EnableCheckpointRestore)
				}
			}
		})
	}
}

func TestEnableCRIUCompatibility(t *testing.T) {
	testCases := []struct {
		name         string
		tomlStr      string
		expected     bool
		expectedWarn bool
	}{
		{
			name:         "deprecated option true",
			tomlStr:      "enable_criu = true",
			expected:     true,
			expectedWarn: true,
		},
		{
			name:         "deprecated option false overrides default",
			tomlStr:      "enable_criu = false",
			expected:     false,
			expectedWarn: true,
		},
		{
			name:     "replacement option",
			tomlStr:  "enable_checkpoint_restore = false",
			expected: false,
		},
		{
			name:         "matching options",
			tomlStr:      "enable_criu = false\nenable_checkpoint_restore = false",
			expected:     false,
			expectedWarn: true,
		},
		{
			name:         "deprecated option takes precedence",
			tomlStr:      "enable_criu = false\nenable_checkpoint_restore = true",
			expected:     false,
			expectedWarn: true,
		},
		{
			name:     "neither option retains default",
			tomlStr:  "",
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultRuntimeConfig()
			require.NoError(t, toml.Unmarshal([]byte(tc.tomlStr), &cfg))

			warnings, err := ValidateRuntimeConfig(context.Background(), &cfg)
			require.NoError(t, err)
			if tc.expectedWarn {
				assert.Contains(t, warnings, deprecation.CRIEnableCRIU)
			} else {
				assert.NotContains(t, warnings, deprecation.CRIEnableCRIU)
			}
			require.NotNil(t, cfg.EnableCheckpointRestore)
			assert.Equal(t, tc.expected, *cfg.EnableCheckpointRestore)
			assert.Nil(t, cfg.EnableCRIU)
		})
	}
}

func TestJSONEnableCheckpointRestore(t *testing.T) {
	jsonStr := `{"enableCheckpointRestore": false}`
	cfg := DefaultRuntimeConfig()
	err := json.Unmarshal([]byte(jsonStr), &cfg)
	assert.NoError(t, err)
	if assert.NotNil(t, cfg.EnableCheckpointRestore) {
		assert.False(t, *cfg.EnableCheckpointRestore)
	}

	b, err := json.Marshal(cfg)
	assert.NoError(t, err)
	assert.Contains(t, string(b), `"enableCheckpointRestore":false`)
}
