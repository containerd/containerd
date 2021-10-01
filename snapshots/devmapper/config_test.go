//go:build linux
// +build linux

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

package devmapper

import (
	"os"
	"testing"

	"github.com/hashicorp/go-multierror"
	"github.com/pelletier/go-toml"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func TestLoadConfig(t *testing.T) {
	expected := Config{
		RootPath:      "/tmp",
		PoolName:      "test",
		BaseImageSize: "128Mb",
	}

	file, err := os.CreateTemp("", "devmapper-config-")
	assert.NilError(t, err)

	encoder := toml.NewEncoder(file)
	err = encoder.Encode(&expected)
	assert.NilError(t, err)

	defer func() {
		err := file.Close()
		assert.NilError(t, err)

		err = os.Remove(file.Name())
		assert.NilError(t, err)
	}()

	loaded, err := LoadConfig(file.Name())
	assert.NilError(t, err)

	assert.Equal(t, loaded.RootPath, expected.RootPath)
	assert.Equal(t, loaded.PoolName, expected.PoolName)
	assert.Equal(t, loaded.BaseImageSize, expected.BaseImageSize)

	assert.Assert(t, loaded.BaseImageSizeBytes == 128*1024*1024)
}

func TestLoadConfigInvalidPath(t *testing.T) {
	_, err := LoadConfig("")
	assert.Equal(t, os.ErrNotExist, err)

	_, err = LoadConfig("/dev/null")
	assert.Assert(t, err != nil)
}

func TestParseInvalidData(t *testing.T) {
	config := Config{
		BaseImageSize: "y",
	}

	err := config.parse()
	assert.Error(t, err, "failed to parse base image size: 'y': invalid size: 'y'")
}

func TestFieldValidation(t *testing.T) {
	config := &Config{}
	err := config.Validate()
	assert.Assert(t, err != nil)

	multErr := (err).(*multierror.Error)
	assert.Assert(t, is.Len(multErr.Errors, 4))

	assert.Assert(t, multErr.Errors[0] != nil, "pool_name is empty")
	assert.Assert(t, multErr.Errors[1] != nil, "root_path is empty")
	assert.Assert(t, multErr.Errors[2] != nil, "base_image_size is empty")
	assert.Assert(t, multErr.Errors[3] != nil, "filesystem type cannot be empty")
}

func TestExistingPoolFieldValidation(t *testing.T) {
	config := &Config{
		PoolName:       "test",
		RootPath:       "test",
		BaseImageSize:  "10mb",
		FileSystemType: "ext4",
	}

	err := config.Validate()
	assert.NilError(t, err)
}
