package namespaces

import (
	"os"

	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

const (
	NamespaceEnvVar = "CONTAINERD_NAMESPACE"
	Default         = "default"
)

var (
	errNamespaceRequired = errors.New("namespace is required")
)

type namespaceKey struct{}

// WithNamespace sets a given namespace on the context
func WithNamespace(ctx context.Context, namespace string) context.Context {
	ctx = context.WithValue(ctx, namespaceKey{}, namespace) // set our key for namespace

	// also store on the grpc headers so it gets picked up by any clients that
	// are using this.
	return withGRPCNamespaceHeader(ctx, namespace)
}

// NamespaceFromEnv uses the namespace defined in CONTAINERD_NAMESPACE or
// default
func NamespaceFromEnv(ctx context.Context) context.Context {
	namespace := os.Getenv(NamespaceEnvVar)
	if namespace == "" {
		namespace = Default
	}
	return WithNamespace(ctx, namespace)
}

// Namespace returns the namespace from the context.
//
// The namespace is not guaranteed to be valid.
func Namespace(ctx context.Context) (string, bool) {
	namespace, ok := ctx.Value(namespaceKey{}).(string)
	if !ok {
		return fromGRPCHeader(ctx)
	}

	return namespace, ok
}

// IsNamespaceRequired returns whether an error is caused by a missing namespace
func IsNamespaceRequired(err error) bool {
	return errors.Cause(err) == errNamespaceRequired
}

// NamespaceRequired returns the valid namepace from the context or an error.
func NamespaceRequired(ctx context.Context) (string, error) {
	namespace, ok := Namespace(ctx)
	if !ok || namespace == "" {
		return "", errNamespaceRequired
	}

	if err := Validate(namespace); err != nil {
		return "", err
	}

	return namespace, nil
}
