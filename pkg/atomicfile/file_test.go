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

package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFile(t *testing.T) {
	const content = "this is some test content for a file"
	dir := t.TempDir()
	path := filepath.Join(dir, "test-file")

	f, err := New(path, 0o644)
	require.NoError(t, err, "failed to create file")
	n, err := fmt.Fprint(f, content)
	assert.NoError(t, err, "failed to write content")
	assert.Equal(t, len(content), n, "written bytes should be equal")
	err = f.Close()
	require.NoError(t, err, "failed to close file")

	actual, err := os.ReadFile(path)
	assert.NoError(t, err, "failed to read file")
	assert.Equal(t, content, string(actual))
}

func TestConcurrentWrites(t *testing.T) {
	const content1 = "this is the first content of the file. there should be none other."
	const content2 = "the second content of the file should win!"
	dir := t.TempDir()
	path := filepath.Join(dir, "test-file")

	file1, err := New(path, 0o600)
	require.NoError(t, err, "failed to create file1")
	file2, err := New(path, 0o644)
	require.NoError(t, err, "failed to create file2")

	n, err := fmt.Fprint(file1, content1)
	assert.NoError(t, err, "failed to write content1")
	assert.Equal(t, len(content1), n, "written bytes should be equal")

	n, err = fmt.Fprint(file2, content2)
	assert.NoError(t, err, "failed to write content2")
	assert.Equal(t, len(content2), n, "written bytes should be equal")

	err = file1.Close()
	require.NoError(t, err, "failed to close file1")
	actual, err := os.ReadFile(path)
	assert.NoError(t, err, "failed to read file")
	assert.Equal(t, content1, string(actual))

	err = file2.Close()
	require.NoError(t, err, "failed to close file2")
	actual, err = os.ReadFile(path)
	assert.NoError(t, err, "failed to read file")
	assert.Equal(t, content2, string(actual))
}
