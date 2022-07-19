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

package local

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/transfer"
	"github.com/containerd/containerd/remotes"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type ProgressTracker struct {
	root  string
	cs    content.Store
	added chan jobUpdate
	waitC chan struct{}

	parents map[digest.Digest][]ocispec.Descriptor
	parentL sync.Mutex
}

type jobState uint8

const (
	jobAdded jobState = iota
	jobInProgress
	jobComplete
)

type jobStatus struct {
	state    jobState
	name     string
	parents  []string
	progress int64
	desc     ocispec.Descriptor
}

type jobUpdate struct {
	desc   ocispec.Descriptor
	exists bool
	//children []ocispec.Descriptor
}

// NewProgressTracker tracks content download progress
func NewProgressTracker(root string, cs content.Store) *ProgressTracker {
	return &ProgressTracker{
		root:    root,
		cs:      cs,
		added:   make(chan jobUpdate, 1),
		waitC:   make(chan struct{}),
		parents: map[digest.Digest][]ocispec.Descriptor{},
	}
}

func (j *ProgressTracker) HandleProgress(ctx context.Context, pf transfer.ProgressFunc) {
	defer close(j.waitC)
	// Instead of ticker, just delay
	jobs := map[digest.Digest]*jobStatus{}
	tc := time.NewTicker(time.Millisecond * 200)
	for {
		select {
		case update := <-j.added:
			job, ok := jobs[update.desc.Digest]
			if !ok {

				// Only captures the parents defined before,
				// could handle parent updates in same thread
				// if there is a synchronization issue
				var parents []string
				j.parentL.Lock()
				for _, parent := range j.parents[update.desc.Digest] {
					parents = append(parents, remotes.MakeRefKey(ctx, parent))
				}
				j.parentL.Unlock()
				if len(parents) == 0 {
					parents = []string{j.root}
				}
				name := remotes.MakeRefKey(ctx, update.desc)

				job = &jobStatus{
					state:   jobAdded,
					name:    name,
					parents: parents,
					desc:    update.desc,
				}
				jobs[update.desc.Digest] = job
				pf(transfer.Progress{
					Event:   "waiting",
					Name:    name,
					Parents: parents,
					//Digest:   desc.Digest.String(),
					Progress: 0,
					Total:    update.desc.Size,
				})
			}
			if update.exists {
				pf(transfer.Progress{
					Event:    "already exists",
					Name:     remotes.MakeRefKey(ctx, update.desc),
					Progress: update.desc.Size,
					Total:    update.desc.Size,
				})
				job.state = jobComplete
				job.progress = job.desc.Size
			}

		case <-tc.C:
			// TODO: Filter by references
			active, err := j.cs.ListStatuses(ctx)
			if err != nil {
				log.G(ctx).WithError(err).Error("failed to list statuses for progress")
			}
			sort.Slice(active, func(i, j int) bool {
				return active[i].Ref < active[j].Ref
			})

			for dgst, job := range jobs {
				if job.state != jobComplete {
					idx := sort.Search(len(active), func(i int) bool { return active[i].Ref >= job.name })
					if idx < len(active) && active[idx].Ref == job.name {
						if active[idx].Offset > job.progress {
							pf(transfer.Progress{
								Event:   "downloading",
								Name:    job.name,
								Parents: job.parents,
								//Digest:   job.desc.Digest.String(),
								Progress: active[idx].Offset,
								Total:    active[idx].Total,
							})
							job.progress = active[idx].Offset
							job.state = jobInProgress
							jobs[dgst] = job
						}
					} else {
						_, err := j.cs.Info(ctx, job.desc.Digest)
						if err == nil {
							pf(transfer.Progress{
								Event:   "complete",
								Name:    job.name,
								Parents: job.parents,
								//Digest:   job.desc.Digest.String(),
								Progress: job.desc.Size,
								Total:    job.desc.Size,
							})

						}
						job.state = jobComplete
						jobs[dgst] = job
					}
				}
			}
			// Next timer?
		case <-ctx.Done():
			return
		}
	}
}

// Add adds a descriptor to be tracked
func (j *ProgressTracker) Add(desc ocispec.Descriptor) {
	if j == nil {
		return
	}
	j.added <- jobUpdate{
		desc: desc,
	}
}

func (j *ProgressTracker) MarkExists(desc ocispec.Descriptor) {
	if j == nil {
		return
	}
	j.added <- jobUpdate{
		desc:   desc,
		exists: true,
	}

}

// Adds hierarchy information
func (j *ProgressTracker) AddChildren(desc ocispec.Descriptor, children []ocispec.Descriptor) {
	if j == nil || len(children) == 0 {
		return
	}
	j.parentL.Lock()
	defer j.parentL.Unlock()
	for _, child := range children {
		j.parents[child.Digest] = append(j.parents[child.Digest], desc)
	}

}

func (j *ProgressTracker) Wait() {
	// timeout rather than rely on cancel
	timeout := time.After(10 * time.Second)
	select {
	case <-timeout:
	case <-j.waitC:
	}
}
