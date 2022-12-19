/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package process

import (
	google_protobuf "github.com/containerd/containerd/protobuf/types"
)

// CreateConfig hold task creation configuration
type CreateConfig struct {
	ID               string
	Bundle           string
	Runtime          string
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
	WorkDir                  string
	Path                     string
	Exit                     bool
	AllowOpenTCP             bool
	AllowExternalUnixSockets bool
	AllowTerminal            bool
	FileLocks                bool
	EmptyNamespaces          []string
}
