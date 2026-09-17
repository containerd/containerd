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

package sandboxfiles

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolvConfContent(t *testing.T) {
	for _, test := range []struct {
		desc     string
		servers  []string
		searches []string
		options  []string
		expected string
	}{
		{
			desc:     "empty dns options should return empty content",
			expected: "",
		},
		{
			desc:     "non-empty dns options should return correct content",
			servers:  []string{"8.8.8.8", "8.8.4.4"},
			searches: []string{"114.114.114.114"},
			options:  []string{"timeout:1"},
			expected: "search 114.114.114.114\nnameserver 8.8.8.8\nnameserver 8.8.4.4\noptions timeout:1\n",
		},
		{
			desc:     "servers only",
			servers:  []string{"1.1.1.1"},
			expected: "nameserver 1.1.1.1\n",
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			assert.Equal(t, test.expected, string(ResolvConfContent(test.servers, test.searches, test.options)))
		})
	}
}

func TestHostnameContent(t *testing.T) {
	assert.Equal(t, "pod-1\n", string(HostnameContent("pod-1")))
}

func TestShmMountData(t *testing.T) {
	assert.Equal(t, "mode=1777,size=67108864", ShmMountData(0))
	assert.Equal(t, "mode=1777,size=1024", ShmMountData(1024))
}
