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

package archive

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/internal/testutil"
	"github.com/containerd/containerd/v2/pkg/archive/compression"
)

// TestPrefixHeaderReadable tests that files that could be created with the
// version of this package that was built with <=go17 are still readable.
func TestPrefixHeaderReadable(t *testing.T) {
	testutil.RequiresRoot(t)

	// https://gist.github.com/stevvooe/e2a790ad4e97425896206c0816e1a882#file-out-go
	var testFile = []byte("\x1f\x8b\x08\x08\x44\x21\x68\x59\x00\x03\x74\x2e\x74\x61\x72\x00\x4b\xcb\xcf\x67\xa0\x35\x30\x80\x00\x86\x06\x10\x47\x01\xc1\x37\x40\x00\x54\xb6\xb1\xa1\xa9\x99\x09\x48\x25\x1d\x40\x69\x71\x49\x62\x91\x02\xe5\x76\xa1\x79\x84\x21\x91\xd6\x80\x72\xaf\x8f\x82\x51\x30\x0a\x46\x36\x00\x00\xf0\x1c\x1e\x95\x00\x06\x00\x00")

	tmpDir := t.TempDir()

	r, err := compression.DecompressStream(bytes.NewReader(testFile))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_, err = Apply(context.Background(), tmpDir, r, WithNoSameOwner())
	if err != nil {
		t.Fatal(err)
	}

	baseName := "foo"
	pth := strings.Repeat("a", 100-len(baseName)) + "/" + baseName

	_, err = os.Lstat(filepath.Join(tmpDir, pth))
	if err != nil {
		t.Fatal(err)
	}
}
