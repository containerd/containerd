//go:build !windows

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
	"reflect"
	"runtime"
	"testing"

	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestDefault(t *testing.T) {
	expected := specs.Platform{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		Variant:      cpuVariant(),
	}
	p := DefaultSpec()
	if !reflect.DeepEqual(p, expected) {
		t.Fatalf("default platform not as expected: %#v != %#v", p, expected)
	}

	s := DefaultString()
	if s != Format(p) {
		t.Fatalf("default specifier should match formatted default spec: %v != %v", s, p)
	}
}
