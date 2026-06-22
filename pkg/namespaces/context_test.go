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
	"testing"
)

func TestContext(t *testing.T) {
	ctx := context.Background()
	namespace, ok := Namespace(ctx)
	if ok {
		t.Fatal("namespace should not be present")
	}

	if namespace != "" {
		t.Fatalf("namespace should not be defined: got %q", namespace)
	}

	expected := "test"
	nctx := WithNamespace(ctx, expected)

	namespace, ok = Namespace(nctx)
	if !ok {
		t.Fatal("expected to find a namespace")
	}

	if namespace != expected {
		t.Fatalf("unexpected namespace: %q != %q", namespace, expected)
	}
}

func TestNamespaceFromEnv(t *testing.T) {
	ctx := context.Background()
	namespace, ok := Namespace(ctx)
	if ok {
		t.Fatal("namespace should not be present")
	}

	if namespace != "" {
		t.Fatalf("namespace should not be defined: got %q", namespace)
	}

	expected := "test-namespace"
	t.Setenv(NamespaceEnvVar, expected)
	nctx := NamespaceFromEnv(ctx)

	namespace, ok = Namespace(nctx)
	if !ok {
		t.Fatal("expected to find a namespace")
	}

	if namespace != expected {
		t.Fatalf("unexpected namespace: %q != %q", namespace, expected)
	}
}
