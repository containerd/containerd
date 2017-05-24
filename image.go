package containerd

import "github.com/containerd/containerd/images"

type Image struct {
	client *Client

	i images.Image
}
