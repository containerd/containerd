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

package commands

import (
	"github.com/pkg/errors"
)

var (
	// ErrArgConfigFile is returned when the configuration for a spec is provided
	ErrArgConfigFile = errors.New("with spec config file, only container id should be provided")
	// ErrUnprovidedImageRef is returned when no image reference is provided
	ErrUnprovidedImageRef = errors.New("image ref must be provided")
	// ErrEmptyContainerID is returned when no container id is provided
	ErrEmptyContainerID = errors.New("container id must be provided")
	// ErrDeleteNoneContainer is returned when no container ids are provided for deletion
	ErrDeleteNoneContainer = errors.New("must specify at least one container to delete")
)
