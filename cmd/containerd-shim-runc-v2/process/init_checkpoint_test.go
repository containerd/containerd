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
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/pkg/stdio"
	runc "github.com/containerd/go-runc"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
)

func TestCheckpointRestorePreservesFileLocks(t *testing.T) {
	bundle := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bundle, "config.json"), []byte(`{}`), 0o600))
	p := &Init{Bundle: bundle, CriuWorkPath: "/work", NoPivotRoot: true}
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

func TestCheckpointRestoreSELinuxOptions(t *testing.T) {
	const (
		processLabel = "system_u:system_r:container_t:s0:c43,c692"
		mountLabel   = "system_u:object_r:container_file_t:s0:c43,c692"
	)
	for _, test := range []struct {
		name string
		spec specs.Spec
		args []string
	}{
		{name: "no process or linux"},
		{name: "empty labels", spec: specs.Spec{Process: &specs.Process{}, Linux: &specs.Linux{}}},
		{
			name: "process only",
			spec: specs.Spec{Process: &specs.Process{SelinuxLabel: processLabel}},
			args: []string{"--lsm-profile=selinux:" + processLabel},
		},
		{
			name: "mount only",
			spec: specs.Spec{Linux: &specs.Linux{MountLabel: mountLabel}},
			args: []string{"--lsm-mount-context=" + mountLabel},
		},
		{
			name: "process and mount",
			spec: specs.Spec{Process: &specs.Process{SelinuxLabel: processLabel}, Linux: &specs.Linux{MountLabel: mountLabel}},
			args: []string{"--lsm-profile=selinux:" + processLabel, "--lsm-mount-context=" + mountLabel},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), "work")
			opts := &runc.RestoreOpts{CheckpointOpts: runc.CheckpointOpts{WorkDir: parent}}
			var labeledPath string
			err := prepareCheckpointRestore(opts, &test.spec, func(path, gotLabel string, shared bool) error {
				labeledPath = path
				require.Equal(t, mountLabel, gotLabel)
				require.False(t, shared)
				info, err := os.Stat(path)
				require.NoError(t, err)
				require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
				return nil
			})
			require.NoError(t, err)
			require.Equal(t, test.args, opts.ExtraArgs)
			if test.spec.Linux == nil || test.spec.Linux.MountLabel == "" {
				require.Empty(t, labeledPath)
				require.Equal(t, parent, opts.WorkDir)
				require.NoDirExists(t, parent)
			} else {
				require.Equal(t, parent, filepath.Dir(labeledPath))
				require.Equal(t, labeledPath, opts.WorkDir)
			}
		})
	}
}

func TestCheckpointRestoreWorkDirectoryIsolation(t *testing.T) {
	parent := t.TempDir()
	sibling := filepath.Join(parent, "restore.log")
	require.NoError(t, os.WriteFile(sibling, []byte("another restore"), 0o600))
	spec := &specs.Spec{Linux: &specs.Linux{MountLabel: "target mount label"}}
	var paths []string
	for range 2 {
		opts := &runc.RestoreOpts{CheckpointOpts: runc.CheckpointOpts{WorkDir: parent}}
		require.NoError(t, prepareCheckpointRestore(opts, spec, func(path, _ string, _ bool) error {
			require.Equal(t, parent, filepath.Dir(path))
			return nil
		}))
		paths = append(paths, opts.WorkDir)
	}
	require.NotEqual(t, paths[0], paths[1])
	data, err := os.ReadFile(sibling)
	require.NoError(t, err)
	require.Equal(t, "another restore", string(data))
}

func TestCheckpointRestorePreparationFailure(t *testing.T) {
	for _, test := range []string{"work parent is file", "missing work ancestor", "relabel fails"} {
		t.Run(test, func(t *testing.T) {
			dir := t.TempDir()
			parent := filepath.Join(dir, "work")
			if test == "work parent is file" {
				require.NoError(t, os.WriteFile(parent, []byte("preserve"), 0o600))
			} else if test == "missing work ancestor" {
				parent = filepath.Join(dir, "missing", "work")
			}
			opts := &runc.RestoreOpts{CheckpointOpts: runc.CheckpointOpts{WorkDir: parent}}
			spec := &specs.Spec{Linux: &specs.Linux{MountLabel: "target mount label"}}
			labelErr := errors.New("relabel failed")
			var labeledPath string
			err := prepareCheckpointRestore(opts, spec, func(path, _ string, _ bool) error {
				labeledPath = path
				return labelErr
			})
			require.Error(t, err)
			require.Equal(t, parent, opts.WorkDir)
			if test == "relabel fails" {
				require.ErrorIs(t, err, labelErr)
				require.NoDirExists(t, labeledPath)
				entries, err := os.ReadDir(parent)
				require.NoError(t, err)
				require.Empty(t, entries)
			} else {
				require.Empty(t, labeledPath)
			}
		})
	}
}

func TestCheckpointRestoreRejectsUnreadableSpec(t *testing.T) {
	for _, contents := range []string{"", "{"} {
		t.Run(contents, func(t *testing.T) {
			bundle := t.TempDir()
			if contents != "" {
				require.NoError(t, os.WriteFile(filepath.Join(bundle, "config.json"), []byte(contents), 0o600))
			}
			p := New("container", nil, stdio.Stdio{})
			p.Bundle = bundle
			p.CriuWorkPath = filepath.Join(bundle, "work")
			state := p.initState
			require.ErrorContains(t, p.createCheckpointedState(&CreateConfig{Checkpoint: "/checkpoint"}, newPidFile(bundle)), "failed to read OCI spec for restore")
			require.Same(t, state, p.initState)
			require.Empty(t, p.restoreWorkPath)
			require.NoDirExists(t, p.CriuWorkPath)
		})
	}
}

func TestCheckpointRestoreDeleteWorkDirectory(t *testing.T) {
	for _, test := range []struct {
		name       string
		attempted  bool
		failDelete bool
	}{
		{name: "before start"},
		{name: "failed restore", attempted: true},
		{name: "failed delete", failDelete: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, dir := newPauseTestProcess(t, "running")
			p.Bundle = dir
			p.CriuWorkPath = filepath.Join(dir, "work")
			opts := &runc.RestoreOpts{CheckpointOpts: runc.CheckpointOpts{WorkDir: p.CriuWorkPath}}
			spec := &specs.Spec{Linux: &specs.Linux{MountLabel: "target mount label"}}
			require.NoError(t, prepareCheckpointRestore(opts, spec, func(string, string, bool) error { return nil }))
			p.restoreWorkPath = opts.WorkDir
			p.initState = &createdCheckpointState{p: p, opts: opts}
			if test.attempted {
				script := `#!/bin/sh
case "$1" in
restore)
	shift
	while [ "$#" -gt 0 ]; do
		if [ "$1" = --work-path ]; then
			shift
			printf 'restore diagnostic' > "$1/restore.log"
		fi
		shift
	done
	exit 1
	;;
delete) exit 0 ;;
*) exit 1 ;;
esac
`
				require.NoError(t, os.WriteFile(p.runtime.Command, []byte(script), 0o700))
				require.ErrorContains(t, p.Start(t.Context()), "OCI runtime restore failed")
			}
			if test.failDelete {
				writePauseTestControl(t, dir, "delete-error")
				require.ErrorContains(t, p.Delete(t.Context()), "failed to delete task")
				require.DirExists(t, opts.WorkDir)
				require.IsType(t, &createdCheckpointState{}, p.initState)
				require.NoError(t, os.Remove(filepath.Join(dir, "delete-error")))
			}
			require.NoError(t, p.Delete(t.Context()))
			if test.attempted {
				data, err := os.ReadFile(filepath.Join(opts.WorkDir, "restore.log"))
				require.NoError(t, err)
				require.Equal(t, "restore diagnostic", string(data))
			} else {
				require.NoDirExists(t, opts.WorkDir)
			}
			require.DirExists(t, p.CriuWorkPath)
		})
	}
}
