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

package server

import (
	"testing"

	srvconfig "github.com/containerd/containerd/services/server/config"
	"github.com/stretchr/testify/assert"
)

const testPath = "/tmp/path/for/testing"

func TestCreateTopLevelDirectoriesErrorsWithSamePathForRootAndState(t *testing.T) {
	path := testPath
	err := CreateTopLevelDirectories(&srvconfig.Config{
		Root:  path,
		State: path,
	})
	assert.EqualError(t, err, "root and state must be different paths")
}

func TestCreateTopLevelDirectoriesWithEmptyStatePath(t *testing.T) {
	statePath := ""
	rootPath := testPath
	err := CreateTopLevelDirectories(&srvconfig.Config{
		Root:  rootPath,
		State: statePath,
	})
	assert.EqualError(t, err, "state must be specified")
}

func TestCreateTopLevelDirectoriesWithEmptyRootPath(t *testing.T) {
	statePath := testPath
	rootPath := ""
	err := CreateTopLevelDirectories(&srvconfig.Config{
		Root:  rootPath,
		State: statePath,
	})
	assert.EqualError(t, err, "root must be specified")
}
