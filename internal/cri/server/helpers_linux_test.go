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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestOpenLogFile(t *testing.T) {
	tests := []struct {
		name    string
		symlink bool
		wantErr error
	}{
		{
			name: "regular file",
		},
		{
			name:    "symlink",
			symlink: true,
			wantErr: unix.ELOOP,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "container.log")
			if test.symlink {
				require.NoError(t, os.Symlink("target", path))
			}

			f, err := openLogFile(path)
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
			} else {
				require.NoError(t, err)
				require.NoError(t, f.Close())
			}
		})
	}
}
