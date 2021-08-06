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

package apply

import (
	"testing"
)

func TestGetOverlayPath(t *testing.T) {
	good := []string{"upperdir=/test/upper", "lowerdir=/test/lower1:/test/lower2", "workdir=/test/work"}
	path, parents, err := getOverlayPath(good)
	if err != nil {
		t.Fatalf("Get overlay path failed: %v", err)
	}
	if path != "/test/upper" {
		t.Fatalf("Unexpected upperdir: %q", path)
	}
	if len(parents) != 2 || parents[0] != "/test/lower1" || parents[1] != "/test/lower2" {
		t.Fatalf("Unexpected parents: %v", parents)
	}

	bad := []string{"lowerdir=/test/lower"}
	_, _, err = getOverlayPath(bad)
	if err == nil {
		t.Fatalf("An error is expected")
	}
}

func TestGetAufsPath(t *testing.T) {
	for _, test := range []struct {
		options   []string
		expectErr bool
	}{
		{
			options:   []string{"random:option", "br:/test/rw=rw:/test/ro=ro+wh"},
			expectErr: false,
		},
		{
			options:   []string{"random:option"},
			expectErr: true,
		},
		{
			options:   []string{"br:/test/ro=ro+wh"},
			expectErr: true,
		},
	} {
		path, parents, err := getAufsPath(test.options)
		if test.expectErr {
			if err == nil {
				t.Fatalf("An error is expected")
			}
			continue
		}
		if err != nil {
			t.Fatalf("Get aufs path failed: %v", err)
		}
		if path != "/test/rw" {
			t.Fatalf("Unexpected rw dir: %q", path)
		}
		if len(parents) != 1 || parents[0] != "/test/ro" {
			t.Fatalf("Unexpected parents: %v", parents)
		}

	}
}
