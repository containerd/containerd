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
