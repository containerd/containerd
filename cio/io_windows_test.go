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

package cio

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestNewFifoSetInDir_NoTerminal(t *testing.T) {
	set, err := NewFIFOSetInDir("", t.Name(), false)
	if err != nil {
		t.Fatalf("NewFifoSetInDir failed with: %v", err)
	}

	assert.Assert(t, !set.Terminal, "FIFOSet.Terminal should be false")
	assert.Assert(t, set.Stdin != "", "FIFOSet.Stdin should be set")
	assert.Assert(t, set.Stdout != "", "FIFOSet.Stdout should be set")
	assert.Assert(t, set.Stderr != "", "FIFOSet.Stderr should be set")
}

func TestNewFifoSetInDir_Terminal(t *testing.T) {
	set, err := NewFIFOSetInDir("", t.Name(), true)
	if err != nil {
		t.Fatalf("NewFifoSetInDir failed with: %v", err)
	}

	assert.Assert(t, set.Terminal, "FIFOSet.Terminal should be false")
	assert.Assert(t, set.Stdin != "", "FIFOSet.Stdin should be set")
	assert.Assert(t, set.Stdout != "", "FIFOSet.Stdout should be set")
	assert.Assert(t, set.Stderr == "", "FIFOSet.Stderr should not be set")
}
