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
