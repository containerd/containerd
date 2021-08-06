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

package platforms

import (
	"sort"
	"testing"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
)

func TestMatchComparerMatch(t *testing.T) {
	m := matchComparer{
		defaults: Only(imagespec.Platform{
			Architecture: "amd64",
			OS:           "windows",
		}),
		osVersionPrefix: "10.0.17763",
	}
	for _, test := range []struct {
		platform imagespec.Platform
		match    bool
	}{
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
				OSVersion:    "10.0.17763.1",
			},
			match: true,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
				OSVersion:    "10.0.17763.2",
			},
			match: true,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
				OSVersion:    "10.0.17762.1",
			},
			match: false,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
				OSVersion:    "10.0.17764.1",
			},
			match: false,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
			},
			match: false,
		},
	} {
		assert.Equal(t, test.match, m.Match(test.platform))
	}
}

func TestMatchComparerLess(t *testing.T) {
	m := matchComparer{
		defaults: Only(imagespec.Platform{
			Architecture: "amd64",
			OS:           "windows",
		}),
		osVersionPrefix: "10.0.17763",
	}
	platforms := []imagespec.Platform{
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17764.1",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17763.1",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17763.2",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17762.1",
		},
	}
	expected := []imagespec.Platform{
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17763.2",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17763.1",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17764.1",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17762.1",
		},
	}
	sort.SliceStable(platforms, func(i, j int) bool {
		return m.Less(platforms[i], platforms[j])
	})
	assert.Equal(t, expected, platforms)
}
