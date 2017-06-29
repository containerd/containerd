package events

func (c *ContainerCreate) Field(fieldpath []string) (string, bool) {
	if len(fieldpath) == 0 {
		return "", false
	}
	switch fieldpath[0] {
	case "container_id":
		return c.ContainerID, len(c.ContainerID) > 0
	case "image":
		return c.Image, len(c.Image) > 0
	case "runtime":
		return c.Runtime.Name, len(c.Runtime.Name) > 0
	}

	return "", false
}

func (c *ContainerUpdate) Field(fieldpath []string) (string, bool) {
	if len(fieldpath) == 0 {
		return "", false
	}
	switch fieldpath[0] {
	case "container_id":
		return c.ContainerID, len(c.ContainerID) > 0
	case "image":
		return c.Image, len(c.Image) > 0
	case "rootfs":
		return c.RootFS, len(c.RootFS) > 0
	case "labels":
		return checkMap(fieldpath[1:], c.Labels)
	}

	return "", false
}

func (c *ContainerDelete) Field(fieldpath []string) (string, bool) {
	if len(fieldpath) == 0 {
		return "", false
	}
	switch fieldpath[0] {
	case "container_id":
		return c.ContainerID, len(c.ContainerID) > 0
	case "image":
		return c.Image, len(c.Image) > 0
	}

	return "", false
}
