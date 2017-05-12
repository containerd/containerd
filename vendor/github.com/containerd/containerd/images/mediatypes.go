package images

// mediatype definitions for image components handled in containerd.
//
// oci components are generally referenced directly, although we may centralize
// here for clarity.
const (
	MediaTypeDockerSchema2Layer        = "application/vnd.docker.image.rootfs.diff.tar"
	MediaTypeDockerSchema2LayerGzip    = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	MediaTypeDockerSchema2Config       = "application/vnd.docker.container.image.v1+json"
	MediaTypeDockerSchema2Manifest     = "application/vnd.docker.distribution.manifest.v2+json"
	MediaTypeDockerSchema2ManifestList = "application/vnd.docker.distribution.manifest.list.v2+json"
)
