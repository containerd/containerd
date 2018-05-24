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

package testutil

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

var rootEnabled bool

func init() {
	flag.BoolVar(&rootEnabled, "test.root", false, "enable tests that require root")
}

// DumpDir prints the contents of the directory to the testing logger.
//
// Use this in a defer statement from a test that may allocate and exercise a
// temporary directory. Immensely useful for sanity checking and debugging
// failing tests.
//
// One should still test that contents are as expected. This is only a visual
// tool to assist when things don't go your way.
func DumpDir(t *testing.T, root string) {
	if err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			t.Log(fi.Mode(), fmt.Sprintf("%10s", ""), path, "->", target)
		} else if fi.Mode().IsRegular() {
			p, err := ioutil.ReadFile(path)
			if err != nil {
				t.Logf("error reading file: %v", err)
				return nil
			}

			if len(p) > 64 { // just display a little bit.
				p = p[:64]
			}
			t.Log(fi.Mode(), fmt.Sprintf("%10d", fi.Size()), path, "[", strconv.Quote(string(p)), "...]")
		} else {
			t.Log(fi.Mode(), fmt.Sprintf("%10d", fi.Size()), path)
		}

		return nil
	}); err != nil {
		t.Fatalf("error dumping directory: %v", err)
	}
}

// DumpDirOnFailure prints the contents of the directory to the testing logger if
// the test has failed.
func DumpDirOnFailure(t *testing.T, root string) {
	if t.Failed() {
		DumpDir(t, root)
	}
}
