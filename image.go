package containerd

import "github.com/containerd/containerd/images"

type Image interface {
	Name() string
}

var _ = (Image)(&image{})

type image struct {
	client *Client

	i images.Image
}

func (i *image) Name() string {
	return i.i.Name
}
