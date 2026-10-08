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

package images

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestExportCommandStdoutDashArg(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		wantAllPlatforms bool
		wantPlatforms    []string
		wantImages       []string
		wantErr          string
	}{
		{
			name:       "dash out with single image",
			args:       []string{"images", "export", "-", "docker.io/library/busybox:latest"},
			wantImages: []string{"docker.io/library/busybox:latest"},
		},
		{
			name:             "dash out with flags before dash and multiple images",
			args:             []string{"images", "export", "--all-platforms", "--platform", "linux/amd64", "-", "docker.io/library/busybox:latest", "docker.io/library/alpine:latest"},
			wantAllPlatforms: true,
			wantPlatforms:    []string{"linux/amd64"},
			wantImages:       []string{"docker.io/library/busybox:latest", "docker.io/library/alpine:latest"},
		},
		{
			name:             "dash out with flags after dash",
			args:             []string{"images", "export", "-", "--all-platforms", "docker.io/library/busybox:latest"},
			wantAllPlatforms: true,
			wantImages:       []string{"docker.io/library/busybox:latest"},
		},
		{
			name:             "double dash before dash out preserves literal args",
			args:             []string{"images", "export", "--", "-", "--all-platforms"},
			wantAllPlatforms: false,
			wantImages:       []string{"--all-platforms"},
		},
		{
			name:    "dash out with missing image",
			args:    []string{"images", "export", "-"},
			wantErr: "please provide both an output filename and an image reference to export",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var (
				gotOut               string
				gotImages            []string
				capturedAllPlatforms bool
				capturedPlatforms    []string
			)

			exportCopy := *exportCommand
			exportCopy.Flags = make([]cli.Flag, len(exportCommand.Flags))
			for i, f := range exportCommand.Flags {
				switch fl := f.(type) {
				case *cli.BoolFlag:
					cp := *fl
					exportCopy.Flags[i] = &cp
				case *cli.StringSliceFlag:
					cp := *fl
					exportCopy.Flags[i] = &cp
				default:
					exportCopy.Flags[i] = f
				}
			}
			exportCopy.Action = func(ctx context.Context, cmd *cli.Command) error {
				resolvedCmd, out, images, err := resolveExportArgs(ctx, cmd)
				if err != nil {
					return err
				}
				gotOut = out
				gotImages = images
				capturedAllPlatforms = resolvedCmd.Bool("all-platforms")
				capturedPlatforms = resolvedCmd.StringSlice("platform")
				return nil
			}

			parent := *Command
			parent.Commands = []*cli.Command{&exportCopy}

			err := parent.Run(context.Background(), tc.args)
			if tc.wantErr != "" {
				require.EqualError(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "-", gotOut)
			assert.Equal(t, tc.wantImages, gotImages)
			assert.Equal(t, tc.wantAllPlatforms, capturedAllPlatforms)
			if len(tc.wantPlatforms) == 0 {
				assert.Empty(t, capturedPlatforms)
			} else {
				assert.Equal(t, tc.wantPlatforms, capturedPlatforms)
			}
		})
	}
}
