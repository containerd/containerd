package containerd

import (
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/remotes"
	"google.golang.org/grpc"
)

type clientOpts struct {
	defaultns   string
	dialOptions []grpc.DialOption
}

// ClientOpt allows callers to set options on the containerd client
type ClientOpt func(c *clientOpts) error

// WithDefaultNamespace sets the default namespace on the client
//
// Any operation that does not have a namespace set on the context will
// be provided the default namespace
func WithDefaultNamespace(ns string) ClientOpt {
	return func(c *clientOpts) error {
		c.defaultns = ns
		return nil
	}
}

// WithDialOpts allows grpc.DialOptions to be set on the connection
func WithDialOpts(opts []grpc.DialOption) ClientOpt {
	return func(c *clientOpts) error {
		c.dialOptions = opts
		return nil
	}
}

// RemoteOpts allows the caller to set distribution options for a remote
type RemoteOpts func(*Client, *RemoteContext) error

// WithPullUnpack is used to unpack an image after pull. This
// uses the snapshotter, content store, and diff service
// configured for the client.
func WithPullUnpack(client *Client, c *RemoteContext) error {
	c.Unpack = true
	return nil
}

// WithPullSnapshotter specifies snapshotter name used for unpacking
func WithPullSnapshotter(snapshotterName string) RemoteOpts {
	return func(client *Client, c *RemoteContext) error {
		c.Snapshotter = snapshotterName
		return nil
	}
}

// WithSchema1Conversion is used to convert Docker registry schema 1
// manifests to oci manifests on pull. Without this option schema 1
// manifests will return a not supported error.
func WithSchema1Conversion(client *Client, c *RemoteContext) error {
	c.ConvertSchema1 = true
	return nil
}

// WithResolver specifies the resolver to use.
func WithResolver(resolver remotes.Resolver) RemoteOpts {
	return func(client *Client, c *RemoteContext) error {
		c.Resolver = resolver
		return nil
	}
}

// WithImageHandler adds a base handler to be called on dispatch.
func WithImageHandler(h images.Handler) RemoteOpts {
	return func(client *Client, c *RemoteContext) error {
		c.BaseHandlers = append(c.BaseHandlers, h)
		return nil
	}
}
