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

package process

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckpointRestorePreservesFileLocks(t *testing.T) {
	p := &Init{CriuWorkPath: "/work", NoPivotRoot: true}
	config := &CreateConfig{Checkpoint: "/checkpoint", ParentCheckpoint: "/parent"}
	pidFile := newPidFile(t.TempDir())
	require.NoError(t, p.createCheckpointedState(config, pidFile))
	state, ok := p.initState.(*createdCheckpointState)
	require.True(t, ok)
	require.True(t, state.opts.FileLocks, "restore must retain the locks captured by checkpoint")
	require.Equal(t, config.Checkpoint, state.opts.ImagePath)
	require.Equal(t, config.ParentCheckpoint, state.opts.ParentPath)
	require.Equal(t, p.CriuWorkPath, state.opts.WorkDir)
	require.Equal(t, pidFile.Path(), state.opts.PidFile)
	require.True(t, state.opts.NoPivot)
}
