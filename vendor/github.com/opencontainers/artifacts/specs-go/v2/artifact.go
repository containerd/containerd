package v2

import "github.com/opencontainers/artifacts/specs-go"

// Artifact describes a registry artifact.
// This structure provides `application/vnd.oci.artifact.manifest.v1+json` mediatype when marshalled to JSON.
type Artifact struct {
	specs.Versioned

	// MediaType is the media type of the object this schema refers to.
	MediaType string `json:"mediaType"`

	// ArtifactType is the artifact type of the object this schema refers to.
	ArtifactType string `json:"artifactType"`

	// Config references the configuration of the object this schema refers to. It is optional.
	Config *Descriptor `json:"config,omitempty"`

	// Blobs is a collection of blobs referenced by this manifest.
	Blobs []Descriptor `json:"blobs"`

	// Manifests is a collection of manifests this artifact is linked to.
	Manifests []Descriptor `json:"manifests"`

	// Annotations contains arbitrary metadata for the artifact manifest.
	Annotations map[string]string `json:"annotations,omitempty"`
}
