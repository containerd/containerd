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
	"encoding/json"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
)

func TestParseEnableCRIU(t *testing.T) {
	testCases := []struct {
		name         string
		tomlStr      string
		expectedCRIU *bool
	}{
		{
			name: "enable_criu set to true",
			tomlStr: `
enable_criu = true
`,
			expectedCRIU: func() *bool { v := true; return &v }(),
		},
		{
			name: "enable_criu set to false",
			tomlStr: `
enable_criu = false
`,
			expectedCRIU: func() *bool { v := false; return &v }(),
		},
		{
			name: "enable_criu absent",
			tomlStr: `
# empty or other fields
`,
			expectedCRIU: func() *bool { v := true; return &v }(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultRuntimeConfig()
			err := toml.Unmarshal([]byte(tc.tomlStr), &cfg)
			assert.NoError(t, err)
			if tc.expectedCRIU == nil {
				assert.Nil(t, cfg.EnableCRIU)
			} else {
				if assert.NotNil(t, cfg.EnableCRIU) {
					assert.Equal(t, *tc.expectedCRIU, *cfg.EnableCRIU)
				}
			}
		})
	}
}

func TestJSONEnableCRIU(t *testing.T) {
	jsonStr := `{"enableCRIU": false}`
	cfg := DefaultRuntimeConfig()
	err := json.Unmarshal([]byte(jsonStr), &cfg)
	assert.NoError(t, err)
	if assert.NotNil(t, cfg.EnableCRIU) {
		assert.False(t, *cfg.EnableCRIU)
	}

	b, err := json.Marshal(cfg)
	assert.NoError(t, err)
	assert.Contains(t, string(b), `"enableCRIU":false`)
}
