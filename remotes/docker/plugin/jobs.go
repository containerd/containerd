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

package plugin

import (
	"sync"

	api "github.com/containerd/containerd/api/services/remotes/v1"
	"github.com/containerd/containerd/remotes/docker"
)

type jobTracker struct {
	jobs    map[string]struct{}
	ordered []string
	tracker docker.StatusTracker
	mu      sync.Mutex
}

func newJobTracker(tracker docker.StatusTracker) *jobTracker {
	return &jobTracker{
		jobs:    make(map[string]struct{}),
		tracker: tracker,
	}
}

func (j *jobTracker) add(ref string) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if _, ok := j.jobs[ref]; ok {
		return
	}
	j.ordered = append(j.ordered, ref)
	j.jobs[ref] = struct{}{}
}

func (j *jobTracker) status() []*api.Status {
	j.mu.Lock()
	defer j.mu.Unlock()

	statuses := make([]*api.Status, 0, len(j.ordered))
	for _, name := range j.ordered {
		si := &api.Status{Name: name}
		status, err := j.tracker.GetStatus(name)
		if err != nil {
			si.Action = api.Action_Waiting
		} else {
			si.Offset = status.Offset
			si.Total = status.Total
			si.StartedAt = status.StartedAt
			si.UpdatedAt = status.UpdatedAt
			if status.Offset >= status.Total {
				if status.UploadUUID == "" {
					si.Action = api.Action_Done
				} else {
					si.Action = api.Action_Commit
				}
			} else {
				si.Action = api.Action_Write
			}
		}
		statuses = append(statuses, si)
	}

	return statuses
}
