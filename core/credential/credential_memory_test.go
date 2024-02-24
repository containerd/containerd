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

package credential

import (
	"context"
	"testing"

	transfertypes "github.com/containerd/containerd/v2/api/types/transfer"
)

func Test_memStore(t *testing.T) {
	store := newMemoryStore()
	host := "docker.io"
	expect := Credentials{
		Host:     host,
		Username: "username",
		Secret:   "password",
	}
	// add
	err := store.Store(context.Background(), expect)
	if err != nil {
		t.Fatal(err)
	}

	// get
	got, err := store.Get(context.TODO(), &transfertypes.AuthRequest{
		Host: host,
	})
	if err != nil {
		t.Fatal(err)
	}
	if expect.Username != got.Username || expect.Secret != got.Secret {
		t.Fatalf("store fail, expect %+v, got +%v", expect, got)
	}

	// delete
	err = store.Delete(context.TODO(), expect)
	if err != nil {
		t.Fatal(err)
	}

	// get
	got, err = store.Get(context.TODO(), &transfertypes.AuthRequest{
		Host: host,
	})
	if err == nil {
		t.Fatalf("expect not found, but found %v", got)
	}
}
