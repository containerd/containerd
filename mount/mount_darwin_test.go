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

package mount

import (
	"errors"
	"testing"

	"github.com/containerd/containerd/errdefs"
)

func TestMountDarwin(t *testing.T) {
	testcases := []struct {
		mounts Mount
		err    error
		msg    string
	}{
		{
			mounts: Mount{
				Type:   "nfs",
				Source: "source",
			},
			err: errdefs.ErrNotFound,
			msg: "should fail with non-allowed type",
		},
		{
			mounts: Mount{
				Type:   "blah",
				Source: "source",
			},
			err: errdefs.ErrNotFound,
			msg: "should fail with non-existent binary",
		},
		{
			mounts: Mount{
				Type:   "darwin",
				Source: "source",
			},
			err: nil,
			msg: "should fail with non-existent target",
		},
		{
			mounts: Mount{
				Type:   "9p",
				Source: "source",
			},
			err: errdefs.ErrNotFound,
			msg: "should fail with non-allowed type",
		},
	}

	for _, tc := range testcases {
		err := tc.mounts.Mount("target")
		t.Log(err)
		if tc.mounts.Type == "darwin" {
			if err == nil {
				t.Fatalf("%s: %s", tc.msg, tc.mounts.Type)
			}
		} else if !errors.Is(err, tc.err) {
			t.Fatalf("%s: %s", tc.msg, tc.mounts.Type)
		}
	}
}
