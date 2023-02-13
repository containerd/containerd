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
	"testing"

	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	require.Equal(t, DefaultSpec(), Normalize(DefaultSpec()))
}

func TestFallbackOnOSVersion(t *testing.T) {
	p := specs.Platform{
		OS:           "windows",
		Architecture: "amd64",
		OSVersion:    "99.99.99.99",
	}

	other := specs.Platform{OS: p.OS, Architecture: p.Architecture}

	m := NewMatcher(p)
	require.True(t, m.Match(other))
}
