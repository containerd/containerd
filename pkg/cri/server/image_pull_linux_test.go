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

package server

import (
	"testing"

	"github.com/stretchr/testify/assert"

	criconfig "github.com/containerd/containerd/pkg/cri/config"
)

func TestEncryptedImagePullOpts(t *testing.T) {
	for desc, test := range map[string]struct {
		keyModel     string
		expectedOpts int
	}{
		"node key model should return one unpack opt": {
			keyModel:     criconfig.KeyModelNode,
			expectedOpts: 1,
		},
		"no key model selected should default to node key model": {
			keyModel:     "",
			expectedOpts: 0,
		},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIService()
		c.config.ImageDecryption.KeyModel = test.keyModel
		got := len(c.encryptedImagesPullOpts())
		assert.Equal(t, test.expectedOpts, got)
	}
}
