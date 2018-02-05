package proc

import (
	google_protobuf "github.com/gogo/protobuf/types"
)

// Mount holds filesystem mount configuration
type Mount struct {
	Type    string
	Source  string
	Target  string
	Options []string
}

// CreateConfig hold task creation configuration
type CreateConfig struct {
	ID               string
	Bundle           string
	Runtime          string
	Rootfs           []Mount
	Terminal         bool
	Stdin            string
	Stdout           string
	Stderr           string
	Checkpoint       string
	ParentCheckpoint string
	Options          *google_protobuf.Any
}

// ExecConfig holds exec creation configuration
type ExecConfig struct {
	ID       string
	Terminal bool
	Stdin    string
	Stdout   string
	Stderr   string
	Spec     *google_protobuf.Any
}

// CheckpointConfig holds task checkpoint configuration
type CheckpointConfig struct {
	Path    string
	Options *google_protobuf.Any
}
