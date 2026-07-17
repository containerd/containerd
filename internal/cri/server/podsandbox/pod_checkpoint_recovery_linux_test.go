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

package podsandbox

import (
	"context"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/require"
)

// A task can exit while the daemon is down, or immediately after being resumed.
func TestPodCheckpointRecoveryHandlesExitedTasks(t *testing.T) {
	for _, test := range []struct {
		name        string
		statuses    []containerd.ProcessStatus
		statusErr   error
		wantErr     bool
		wantResumes int
	}{
		{name: "already stopped", statuses: []containerd.ProcessStatus{containerd.Stopped}},
		{name: "exits after resume", statuses: []containerd.ProcessStatus{containerd.Paused, containerd.Stopped}, wantResumes: 1},
		{name: "deleted task", statusErr: errdefs.ErrNotFound},
		{name: "lookup failure", statusErr: errdefs.ErrUnavailable, wantErr: true},
		{name: "unknown state", statuses: []containerd.ProcessStatus{containerd.Unknown}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			task := &exitingCheckpointTask{statuses: test.statuses, statusErr: test.statusErr}
			var events []string
			surviving := &recordingPodCheckpointTask{id: "surviving", status: containerd.Paused, events: &events}
			recovery := podCheckpointTaskRecovery{tasks: []podCheckpointRuntimeTask{task, surviving}}
			err := recovery.recover(t.Context())
			if test.wantErr {
				require.Error(t, err)
				require.Len(t, recovery.tasks, 1, "retain only the task whose recovery is uncertain")
			} else {
				require.NoError(t, err)
				require.Empty(t, recovery.tasks)
			}
			require.Equal(t, test.wantResumes, task.resumes)
			require.Equal(t, []string{"resume:surviving"}, events)
		})
	}
}

type exitingCheckpointTask struct {
	podCheckpointRuntimeTask
	statuses  []containerd.ProcessStatus
	statusErr error
	resumes   int
}

func (*exitingCheckpointTask) ID() string { return "exiting" }

func (t *exitingCheckpointTask) Status(context.Context) (containerd.Status, error) {
	if t.statusErr != nil {
		return containerd.Status{}, t.statusErr
	}
	state := t.statuses[0]
	if len(t.statuses) > 1 {
		t.statuses = t.statuses[1:]
	}
	return containerd.Status{Status: state}, nil
}

func (t *exitingCheckpointTask) Resume(context.Context) error {
	t.resumes++
	return nil
}
