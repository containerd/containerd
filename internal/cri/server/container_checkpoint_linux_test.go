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

package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
)

func TestCheckIfCheckpointOCIImage(t *testing.T) {
	for _, test := range []struct {
		desc        string
		input       string
		localResolve func(string) (imagestore.Image, error)
		expectID    string
		expectErr   bool
	}{
		{
			desc:      "empty input returns empty without error",
			input:     "",
			expectID:  "",
			expectErr: false,
		},
		{
			desc:      "file path returns empty without error",
			input:     createTempFile(t),
			expectID:  "",
			expectErr: false,
		},
		{
			desc:  "LocalResolve NotFound returns empty without error",
			input: "nix:0/nix/store/abc123-image.tar:latest",
			localResolve: func(string) (imagestore.Image, error) {
				return imagestore.Image{}, errdefs.ErrNotFound
			},
			expectID:  "",
			expectErr: false,
		},
		{
			desc:  "LocalResolve other error propagates",
			input: "some-image:latest",
			localResolve: func(string) (imagestore.Image, error) {
				return imagestore.Image{}, errdefs.ErrUnknown
			},
			expectID:  "",
			expectErr: true,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			fake := &fakeImageService{}
			if test.localResolve != nil {
				fake.localResolveFunc = test.localResolve
			}
			c := newTestCRIService(withImageService(fake))
			id, err := c.checkIfCheckpointOCIImage(context.Background(), test.input)
			if test.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, test.expectID, id)
		})
	}
}

func createTempFile(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "checkpoint-test-*")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	return filepath.Join(f.Name())
}
