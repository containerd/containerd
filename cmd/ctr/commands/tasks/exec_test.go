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

package tasks

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestExecCommandProcessArgsWithFlags(t *testing.T) {
	cmd := *execCommand
	var (
		capturedID     string
		capturedArgs   []string
		capturedExecID string
		capturedTTY    bool
		capturedDetach bool
	)

	cmd.Action = func(ctx context.Context, c *cli.Command) error {
		capturedID = c.Args().First()
		capturedArgs = c.Args().Tail()
		capturedExecID = c.String("exec-id")
		capturedTTY = c.Bool("tty")
		capturedDetach = c.Bool("detach")
		return nil
	}

	args := []string{
		"exec",
		"--exec-id", "exec-1",
		"-t",
		"test-container",
		"sh",
		"-uexc",
		"ls -d /tmp",
	}

	err := cmd.Run(context.Background(), args)
	require.NoError(t, err)

	assert.Equal(t, "exec-1", capturedExecID)
	assert.True(t, capturedTTY)
	assert.False(t, capturedDetach)
	assert.Equal(t, "test-container", capturedID)
	assert.Equal(t, []string{"sh", "-uexc", "ls -d /tmp"}, capturedArgs)
}
