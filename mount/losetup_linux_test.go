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
	"os"
	"testing"

	"github.com/containerd/continuity/testutil"
)

var randomData = []byte("randomdata")

func createTempFile(t *testing.T) string {
	t.Helper()

	f, err := os.CreateTemp("", "losetup")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err = f.Truncate(512); err != nil {
		t.Fatal(err)
	}

	return f.Name()
}

func TestNonExistingLoop(t *testing.T) {
	testutil.RequiresRoot(t)

	backingFile := "setup-loop-test-no-such-file"
	_, err := setupLoop(backingFile, LoopParams{})
	if err == nil {
		t.Fatalf("setupLoop with non-existing file should fail")
	}
}

func TestRoLoop(t *testing.T) {
	testutil.RequiresRoot(t)

	backingFile := createTempFile(t)
	defer func() {
		if err := os.Remove(backingFile); err != nil {
			t.Fatal(err)
		}
	}()

	file, err := setupLoop(backingFile, LoopParams{Readonly: true, Autoclear: true})
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	if _, err := file.Write(randomData); err == nil {
		t.Fatalf("writing to readonly loop device should fail")
	}
}

func TestRwLoop(t *testing.T) {
	testutil.RequiresRoot(t)

	backingFile := createTempFile(t)
	defer func() {
		if err := os.Remove(backingFile); err != nil {
			t.Fatal(err)
		}
	}()

	file, err := setupLoop(backingFile, LoopParams{Autoclear: false})
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	if _, err := file.Write(randomData); err != nil {
		t.Fatal(err)
	}
}

func TestAttachDetachLoopDevice(t *testing.T) {
	testutil.RequiresRoot(t)

	path := createTempFile(t)
	defer func() {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}()

	dev, err := AttachLoopDevice(path)
	if err != nil {
		t.Fatal(err)
	}

	if err = DetachLoopDevice(dev); err != nil {
		t.Fatal(err)
	}
}
