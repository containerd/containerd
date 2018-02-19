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
	"runtime"
	"testing"
)

func TestCPUVariant(t *testing.T) {
	if !isArmArch(runtime.GOARCH) || !isLinuxOS(runtime.GOOS) {
		t.Skip("only relevant on linux/arm")
	}

	variants := []string{"v8", "v7", "v6", "v5", "v4", "v3"}

	p := getCPUVariant()
	for _, variant := range variants {
		if p == variant {
			t.Logf("got valid variant as expected: %#v = %#v\n", p, variant)
			return
		}
	}

	t.Fatalf("could not get valid variant as expected: %v\n", variants)
}
