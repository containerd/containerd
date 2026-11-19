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

package run

import (
	"context"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestParseMountFlag(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMount specs.Mount
		wantErr   bool
	}{
		{
			name:  "valid bind mount with options",
			input: "type=bind,src=/tmp,dst=/host,options=rbind:ro",
			wantMount: specs.Mount{
				Type:        "bind",
				Source:      "/tmp",
				Destination: "/host",
				Options:     []string{"rbind", "ro"},
			},
		},
		{
			name:  "valid bind mount using source and destination aliases",
			input: "type=bind,source=/var/data,destination=/data,options=rbind:rw",
			wantMount: specs.Mount{
				Type:        "bind",
				Source:      "/var/data",
				Destination: "/data",
				Options:     []string{"rbind", "rw"},
			},
		},
		{
			name:  "valid tmpfs mount with colon-separated sub-options",
			input: "type=tmpfs,dst=/mnt,options=nosuid:nodev:mode=1777",
			wantMount: specs.Mount{
				Type:        "tmpfs",
				Destination: "/mnt",
				Options:     []string{"nosuid", "nodev", "mode=1777"},
			},
		},
		{
			name:  "valid mount without options",
			input: "type=bind,src=/var/log,dst=/var/log",
			wantMount: specs.Mount{
				Type:        "bind",
				Source:      "/var/log",
				Destination: "/var/log",
			},
		},
		{
			name:    "invalid format missing key=val",
			input:   "type=bind,/tmp,/host",
			wantErr: true,
		},
		{
			name:    "unsupported mount option",
			input:   "type=bind,src=/tmp,dst=/host,invalidkey=foo",
			wantErr: true,
		},
		{
			name:    "malformed csv with unclosed quote",
			input:   `type="bind,src=/tmp`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMountFlag(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantMount, got)
		})
	}
}

func TestRunCommandFlagParsing(t *testing.T) {
	cmd := *Command
	var (
		capturedMounts      []string
		capturedEnvs        []string
		capturedLabels      []string
		capturedAnnotations []string
	)

	cmd.Action = func(ctx context.Context, c *cli.Command) error {
		capturedMounts = c.StringSlice("mount")
		capturedEnvs = c.StringSlice("env")
		capturedLabels = c.StringSlice("label")
		capturedAnnotations = c.StringSlice("annotation")
		return nil
	}

	args := []string{
		"run",
		"--mount", "type=bind,src=/tmp,dst=/host,options=rbind:ro",
		"--mount", "type=tmpfs,dst=/mnt,options=nosuid",
		"--env", "FOO=a,b",
		"--env", "BAR=c,d,e",
		"--label", "app=test,env=dev",
		"--annotation", "org.opencontainers=a,b",
		"image:latest",
		"test-container",
	}

	err := cmd.Run(context.Background(), args)
	require.NoError(t, err)

	// Verify --mount flags were not split on commas
	wantMounts := []string{
		"type=bind,src=/tmp,dst=/host,options=rbind:ro",
		"type=tmpfs,dst=/mnt,options=nosuid",
	}
	assert.Equal(t, wantMounts, capturedMounts)

	// Verify each parsed mount flag can be parsed by parseMountFlag
	for _, m := range capturedMounts {
		_, err := parseMountFlag(m)
		assert.NoError(t, err)
	}

	// Verify --env flags were not split on commas
	wantEnvs := []string{
		"FOO=a,b",
		"BAR=c,d,e",
	}
	assert.Equal(t, wantEnvs, capturedEnvs)

	// Verify --label flags were not split on commas
	wantLabels := []string{
		"app=test,env=dev",
	}
	assert.Equal(t, wantLabels, capturedLabels)

	// Verify --annotation flags were not split on commas
	wantAnnotations := []string{
		"org.opencontainers=a,b",
	}
	assert.Equal(t, wantAnnotations, capturedAnnotations)
}

func TestRunCommandProcessArgsWithFlags(t *testing.T) {
	cmd := *Command
	var (
		capturedRef    string
		capturedID     string
		capturedArgs   []string
		capturedRM     bool
		capturedDetach bool
	)

	cmd.Action = func(ctx context.Context, c *cli.Command) error {
		capturedRef = c.Args().First()
		capturedID = c.Args().Get(1)
		capturedArgs = c.Args().Slice()[2:]
		capturedRM = c.Bool("rm")
		capturedDetach = c.Bool("detach")
		return nil
	}

	args := []string{
		"run",
		"--rm",
		"docker.io/library/busybox:latest",
		"test-container",
		"sh",
		"-uexc",
		"echo -n hello && ls -d /tmp",
	}

	err := cmd.Run(context.Background(), args)
	require.NoError(t, err)

	assert.True(t, capturedRM)
	assert.False(t, capturedDetach)
	assert.Equal(t, "docker.io/library/busybox:latest", capturedRef)
	assert.Equal(t, "test-container", capturedID)
	assert.Equal(t, []string{"sh", "-uexc", "echo -n hello && ls -d /tmp"}, capturedArgs)
}
