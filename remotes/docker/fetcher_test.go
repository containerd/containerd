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

package docker

import (
	"context"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetcherOpen(t *testing.T) {
	content := make([]byte, 128)
	rand.New(rand.NewSource(1)).Read(content)
	start := 0

	s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if start > 0 {
			rw.Header().Set("content-range", fmt.Sprintf("bytes %d-127/128", start))
		}
		rw.Header().Set("content-length", fmt.Sprintf("%d", len(content[start:])))
		rw.Write(content[start:])
	}))
	defer s.Close()

	f := dockerFetcher{&dockerBase{
		client: s.Client(),
	}}
	ctx := context.Background()

	checkReader := func(o int64) {
		t.Helper()
		rc, err := f.open(ctx, s.URL, "", o)
		if err != nil {
			t.Fatalf("failed to open: %+v", err)
		}
		b, err := ioutil.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		expected := content[o:]
		if len(b) != len(expected) {
			t.Errorf("unexpected length %d, expected %d", len(b), len(expected))
			return
		}
		for i, c := range expected {
			if b[i] != c {
				t.Errorf("unexpected byte %x at %d, expected %x", b[i], i, c)
				return
			}
		}

	}

	checkReader(0)

	// Test server ignores content range
	checkReader(25)

	// Use content range on server
	start = 20
	checkReader(20)

	// Check returning just last byte and no bytes
	start = 127
	checkReader(127)
	start = 128
	checkReader(128)

	// Check that server returning a different content range
	// then requested errors
	start = 30
	_, err := f.open(ctx, s.URL, "", 20)
	if err == nil {
		t.Fatal("expected error opening with invalid server response")
	}
}
