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

package command

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestOCIHookStopFlagParsing(t *testing.T) {
	wantArgs := []string{
		"/usr/local/bin/my-hook",
		"--id", "{{id}}",
		"--config", "/etc/hook/config.toml",
		"-l", "debug",
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		{
			name: "without separator",
			args: append([]string{"oci-hook"}, wantArgs...),
		},
		{
			name: "with double-dash separator",
			args: append([]string{"oci-hook", "--"}, wantArgs...),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := *ociHook
			var capturedArgs []string

			cmd.Action = func(ctx context.Context, c *cli.Command) error {
				capturedArgs = c.Args().Slice()
				return nil
			}

			err := cmd.Run(context.Background(), tc.args)
			require.NoError(t, err)
			assert.Equal(t, wantArgs, capturedArgs)
		})
	}
}
