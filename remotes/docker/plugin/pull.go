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
	"context"
	"sync"
	"time"

	"github.com/containerd/containerd"
	api "github.com/containerd/containerd/api/services/remotes/v1"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/service"
	"github.com/opencontainers/go-digest"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
)

func (r *remote) Pull(ctx context.Context, req *api.PullRequest) (_ <-chan *service.PullResponseEnvelope, retErr error) {
	jobs := newPullJobs(r.client.ContentStore(), req.Remote)

	pullOpts := []containerd.RemoteOpt{
		containerd.WithResolver(r.newResolver(ctx, req.Auth, nil)),
		containerd.WithImageHandlerWrapper(func(h images.Handler) images.Handler {
			return images.Handlers(h, images.HandlerFunc(func(ctx context.Context, desc v1.Descriptor) ([]v1.Descriptor, error) {
				jobs.add(desc)
				return nil, nil
			}))
		}),
		func(_ *containerd.Client, o *containerd.RemoteContext) error {
			if req.Platform != nil {
				o.PlatformMatcher = platforms.Only(platformAPIToOCI(*req.Platform))
			} else {
				o.PlatformMatcher = platforms.All
			}

			if req.AllMetadata {
				o.AllMetadata = true
			}
			o.Unpack = req.Unpack
			o.MaxConcurrentDownloads = int(req.MaxConcurrency)
			if req.Snapshotter != "" {
				o.Snapshotter = req.Snapshotter
			}

			return nil
		},
	}
	if req.DiscardContent {
		pullOpts = append(pullOpts, containerd.WithChildLabelMap(images.ChildGCLabelsFilterLayers))
	}

	type result struct {
		i   containerd.Image
		err error
	}
	chPull := make(chan result, 1)
	go func() {
		image, err := r.client.Pull(ctx, req.Remote, pullOpts...)
		chPull <- result{image, err}
	}()

	ch := make(chan *service.PullResponseEnvelope)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)

		defer close(ch)
		defer ticker.Stop()

		var (
			r     result
			done  bool
			img   *api.Image
			start = time.Now()
		)
		for {
			select {
			case <-ticker.C:
			case r = <-chPull:
				done = true
				if r.err == nil {
					img = &api.Image{
						Name:      r.i.Name(),
						Labels:    r.i.Labels(),
						Target:    ociDescriptorToAPI(r.i.Target()),
						CreatedAt: r.i.Metadata().CreatedAt,
						UpdatedAt: r.i.Metadata().UpdatedAt,
					}
				}
			case <-ctx.Done():
				done = true
			}

			status := jobs.status(ctx, start, done)

			select {
			case ch <- &service.PullResponseEnvelope{Err: r.err, PullResponse: &api.PullResponse{Image: img, Statuses: status}}:
				if done {
					return
				}
			case <-ctx.Done():
				timer := time.NewTimer(5 * time.Second)
				if r.err == nil {
					r.err = ctx.Err()
				}
				select {
				case ch <- &service.PullResponseEnvelope{Err: r.err, PullResponse: &api.PullResponse{Image: img, Statuses: status}}:
				case <-timer.C:
				}
				timer.Stop()
				return
			}
		}
	}()

	return ch, nil
}

type pullJobs struct {
	cs content.Store

	statuses map[string]*api.Status

	mu       sync.Mutex
	ls       []v1.Descriptor
	idx      map[digest.Digest]struct{}
	resolved bool
	name     string
}

func newPullJobs(cs content.Store, name string) *pullJobs {
	return &pullJobs{
		name:     name,
		cs:       cs,
		statuses: make(map[string]*api.Status),
		idx:      make(map[digest.Digest]struct{}),
	}
}

func (j *pullJobs) add(desc v1.Descriptor) {
	j.mu.Lock()
	if _, ok := j.idx[desc.Digest]; !ok {
		j.ls = append(j.ls, desc)
		j.idx[desc.Digest] = struct{}{}
	}
	j.resolved = true
	j.mu.Unlock()
}

func (j *pullJobs) IsResolved() bool {
	j.mu.Lock()
	resolved := j.resolved
	j.mu.Unlock()
	return resolved
}

func (j *pullJobs) status(ctx context.Context, start time.Time, done bool) []*api.Status {
	action := api.Action_Resolved
	if !j.IsResolved() {
		action = api.Action_Resolving
	}

	j.statuses[j.name] = &api.Status{Name: j.name, Action: action}
	keys := []string{j.name}

	activeSeen := make(map[string]struct{})
	if !done {
		active, err := j.cs.ListStatuses(ctx, "")
		if err != nil && !errdefs.IsCanceled(err) {
			log.G(ctx).WithError(err).Warn("Could not fetch content status")
			return nil
		}

		for _, s := range active {
			j.statuses[s.Ref] = &api.Status{
				Action:    api.Action_Write,
				StartedAt: s.StartedAt,
				UpdatedAt: s.UpdatedAt,
				Offset:    s.Offset,
				Total:     s.Total,
				Name:      s.Ref,
			}
			activeSeen[s.Ref] = struct{}{}
		}
	}

	jobs := j.jobs()
	for _, desc := range jobs {
		key := remotes.MakeRefKey(ctx, desc)
		keys = append(keys, key)

		if _, ok := activeSeen[key]; ok {
			continue
		}

		status, ok := j.statuses[key]
		if !done && (!ok || status.Action == api.Action_Write) {
			info, err := j.cs.Info(ctx, desc.Digest)
			if err != nil {
				if !errdefs.IsNotFound(err) {
					return nil
				} else {
					status = &api.Status{
						Name:   key,
						Action: api.Action_Waiting,
					}
				}
			} else if info.CreatedAt.After(start) {
				status = &api.Status{
					Action:    api.Action_Done,
					Offset:    info.Size,
					Total:     info.Size,
					UpdatedAt: info.CreatedAt,
					Name:      key,
				}
				j.statuses[key] = status
			} else {
				j.statuses[key] = &api.Status{Name: key, Action: api.Action_Exists}
			}
		} else if done {
			if ok {
				if status.Action != api.Action_Done && status.Action != api.Action_Exists {
					status.Action = api.Action_Done
					j.statuses[key] = status
				}
			} else {
				j.statuses[key] = &api.Status{Name: key, Action: api.Action_Done}
			}
		}
	}

	out := make([]*api.Status, 0, len(jobs))
	for _, key := range keys {
		out = append(out, j.statuses[key])
	}

	return out
}

func (j *pullJobs) jobs() []v1.Descriptor {
	ls := make([]v1.Descriptor, len(j.ls))
	j.mu.Lock()
	copy(ls, j.ls)
	j.mu.Unlock()
	return ls
}
