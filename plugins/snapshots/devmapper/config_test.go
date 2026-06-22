//go:build linux

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

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
)

func TestLoadConfig(t *testing.T) {
	expected := Config{
		RootPath:      "/tmp",
		PoolName:      "test",
		BaseImageSize: "128Mb",
	}

	file, err := os.CreateTemp(t.TempDir(), "devmapper-config-")
	assert.NoError(t, err)
	t.Cleanup(func() {
		file.Close()
	})

	encoder := toml.NewEncoder(file)
	err = encoder.Encode(&expected)
	assert.NoError(t, err)

	loaded, err := LoadConfig(file.Name())
	assert.NoError(t, err)

	assert.Equal(t, loaded.RootPath, expected.RootPath)
	assert.Equal(t, loaded.PoolName, expected.PoolName)
	assert.Equal(t, loaded.BaseImageSize, expected.BaseImageSize)
	assert.True(t, loaded.BaseImageSizeBytes == 128*1024*1024)
}

func TestLoadConfigInvalidPath(t *testing.T) {
	_, err := LoadConfig("")
	assert.Equal(t, os.ErrNotExist, err)

	_, err = LoadConfig("/dev/null")
	assert.NotNil(t, err)
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
	assert.NotNil(t, err)

	multErr := err.(interface{ Unwrap() []error }).Unwrap()
	assert.Len(t, multErr, 4)

	assert.NotNil(t, multErr[0], "pool_name is empty")
	assert.NotNil(t, multErr[1], "root_path is empty")
	assert.NotNil(t, multErr[2], "base_image_size is empty")
	assert.NotNil(t, multErr[3], "filesystem type cannot be empty")
}

func TestExistingPoolFieldValidation(t *testing.T) {
	config := &Config{
		PoolName:       "test",
		RootPath:       "test",
		BaseImageSize:  "10mb",
		FileSystemType: "ext4",
	}

	err := config.Validate()
	assert.NoError(t, err)
}
