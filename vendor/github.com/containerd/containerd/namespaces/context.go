package namespaces

import (
	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

var (
	errNamespaceRequired = errors.New("namespace is required")
)

type namespaceKey struct{}

func WithNamespace(ctx context.Context, namespace string) context.Context {
	ctx = context.WithValue(ctx, namespaceKey{}, namespace) // set our key for namespace

	// also store on the grpc headers so it gets picked up by any clients that
	// are using this.
	return withGRPCNamespaceHeader(ctx, namespace)
}

func Namespace(ctx context.Context) (string, bool) {
	namespace, ok := ctx.Value(namespaceKey{}).(string)
	if !ok {
		return fromGRPCHeader(ctx)
	}

	return namespace, ok
}

func IsNamespaceRequired(err error) bool {
	return errors.Cause(err) == errNamespaceRequired
}

func NamespaceRequired(ctx context.Context) (string, error) {
	namespace, ok := Namespace(ctx)
	if !ok || namespace == "" {
		return "", errNamespaceRequired
	}

	return namespace, nil
}
