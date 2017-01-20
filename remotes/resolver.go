package remotes

import "context"

// Resolver provides a remote based on a locator.
type Resolver interface {
	// Resolve returns a remote from the locator.
	//
	// A locator is a scheme-less URI representing the remote. Structurally, it
	// has a host and path. The "host" can be used to directly reference a
	// specific host or be matched against a specific handler.
	Resolve(ctx context.Context, locator string) (Remote, error)
}

type ResolverFunc func(context.Context, string) (Remote, error)

func (fn ResolverFunc) Resolve(ctx context.Context, locator string) (Remote, error) {
	return fn(ctx, locator)
}
