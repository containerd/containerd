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
	"context"
	"reflect"
	"testing"

	"github.com/containerd/ttrpc"
)

func TestCopyTTRPCMetadata(t *testing.T) {
	src := ttrpc.MD{}
	src.Set("key", "a", "b", "c", "d")
	md := copyMetadata(src)

	if !reflect.DeepEqual(src, md) {
		t.Fatalf("metadata is copied incorrectly")
	}

	slice, _ := src.Get("key")
	slice[0] = "z"
	if reflect.DeepEqual(src, md) {
		t.Fatalf("metadata is copied incorrectly")
	}
}

func TestTTRPCNamespaceHeader(t *testing.T) {
	ctx := context.Background()
	namespace := "test-namespace"
	ctx = withTTRPCNamespaceHeader(ctx, namespace)

	header, ok := fromTTRPCHeader(ctx)
	if !ok || header != namespace {
		t.Fatalf("ttrp namespace header is set incorrectly")
	}
}
