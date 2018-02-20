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

package namespaces

import (
	"strings"
	"testing"

	"github.com/containerd/containerd/errdefs"
)

func TestValidNamespaces(t *testing.T) {
	for _, input := range []string{
		"default",
		"Default",
		t.Name(),
		"default-default",
		"default--default",
		"containerd.io",
		"foo.boo",
		"swarmkit.docker.io",
		"zn--e9.org", // or something like it!
		strings.Repeat("a", maxLength),
	} {
		t.Run(input, func(t *testing.T) {
			if err := Validate(input); err != nil {
				t.Fatalf("unexpected error: %v != nil", err)
			}
		})
	}
}

func TestInvalidNamespaces(t *testing.T) {
	for _, input := range []string{
		".foo..foo",
		"foo/foo",
		"foo/..",
		"foo..foo",
		"foo.-boo",
		"foo.-boo.bar",
		"-foo.boo",
		"foo.boo-",
		"foo_foo.boo_underscores", // boo-urns?
		"0912341234",
		"task.0.0123456789",
		strings.Repeat("a", maxLength+1),
	} {

		t.Run(input, func(t *testing.T) {
			if err := Validate(input); err == nil {
				t.Fatal("expected invalid error")
			} else if !errdefs.IsInvalidArgument(err) {
				t.Fatal("error should be an invalid identifier error")
			}
		})
	}
}
