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

package containers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestContainersCreateCommandFlagParsing(t *testing.T) {
	cmd := *createCommand
	var (
		capturedMounts []string
		capturedEnvs   []string
		capturedLabels []string
	)

	cmd.Action = func(ctx context.Context, c *cli.Command) error {
		capturedMounts = c.StringSlice("mount")
		capturedEnvs = c.StringSlice("env")
		capturedLabels = c.StringSlice("label")
		return nil
	}

	args := []string{
		"create",
		"--mount", "type=bind,src=/tmp,dst=/host,options=rbind:ro",
		"--mount", "type=tmpfs,dst=/mnt,options=nosuid",
		"--env", "FOO=a,b",
		"--env", "BAR=c,d,e",
		"--label", "app=test,env=dev",
		"image:latest",
		"test-container",
	}

	err := cmd.Run(context.Background(), args)
	require.NoError(t, err)

	wantMounts := []string{
		"type=bind,src=/tmp,dst=/host,options=rbind:ro",
		"type=tmpfs,dst=/mnt,options=nosuid",
	}
	assert.Equal(t, wantMounts, capturedMounts)

	wantEnvs := []string{
		"FOO=a,b",
		"BAR=c,d,e",
	}
	assert.Equal(t, wantEnvs, capturedEnvs)

	wantLabels := []string{
		"app=test,env=dev",
	}
	assert.Equal(t, wantLabels, capturedLabels)
}

func TestContainersCommandHierarchyFlagParsing(t *testing.T) {
	cmd := *Command
	var (
		capturedMounts []string
		capturedEnvs   []string
	)

	var subCmds []*cli.Command
	for _, sub := range cmd.Commands {
		subClone := *sub
		if sub.Name == "create" {
			subClone.Action = func(ctx context.Context, c *cli.Command) error {
				capturedMounts = c.StringSlice("mount")
				capturedEnvs = c.StringSlice("env")
				return nil
			}
		}
		subCmds = append(subCmds, &subClone)
	}
	cmd.Commands = subCmds

	args := []string{
		"containers",
		"create",
		"--mount", "type=bind,src=/tmp,dst=/host,options=rbind:ro",
		"--mount", "type=tmpfs,dst=/mnt,options=nosuid",
		"--env", "FOO=a,b",
		"image:latest",
		"test-container",
	}

	err := cmd.Run(context.Background(), args)
	require.NoError(t, err)

	wantMounts := []string{
		"type=bind,src=/tmp,dst=/host,options=rbind:ro",
		"type=tmpfs,dst=/mnt,options=nosuid",
	}
	assert.Equal(t, wantMounts, capturedMounts)

	wantEnvs := []string{
		"FOO=a,b",
	}
	assert.Equal(t, wantEnvs, capturedEnvs)
}

func TestContainersCreateCommandProcessArgsWithFlags(t *testing.T) {
	cmd := *createCommand
	var (
		capturedRef  string
		capturedID   string
		capturedArgs []string
	)

	cmd.Action = func(ctx context.Context, c *cli.Command) error {
		capturedRef = c.Args().First()
		capturedID = c.Args().Get(1)
		capturedArgs = c.Args().Slice()[2:]
		return nil
	}

	args := []string{
		"create",
		"docker.io/library/busybox:latest",
		"test-container",
		"sh",
		"-uexc",
		"echo hello",
	}

	err := cmd.Run(context.Background(), args)
	require.NoError(t, err)

	assert.Equal(t, "docker.io/library/busybox:latest", capturedRef)
	assert.Equal(t, "test-container", capturedID)
	assert.Equal(t, []string{"sh", "-uexc", "echo hello"}, capturedArgs)
}
