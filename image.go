package containerd

import "github.com/containerd/containerd/images"

type Image interface {
}

var _ = (Image)(&image{})

type image struct {
	client *Client

	i images.Image
}
