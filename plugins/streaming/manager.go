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

package streaming

import (
	"context"
	"errors"
	"sync"

	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/gc"
	"github.com/containerd/containerd/v2/leases"
	"github.com/containerd/containerd/v2/metadata"
	"github.com/containerd/containerd/v2/namespaces"
	"github.com/containerd/containerd/v2/pkg/streaming"
	"github.com/containerd/containerd/v2/plugin"
	"github.com/containerd/containerd/v2/plugin/registry"
	"github.com/containerd/containerd/v2/plugins"
)

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.StreamingPlugin,
		ID:   "manager",
		Requires: []plugin.Type{
			plugins.MetadataPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			md, err := ic.Get(plugins.MetadataPlugin)
			if err != nil {
				return nil, err
			}

			sm := &streamManager{
				streams: map[string]map[string]*managedStream{},
				byLease: map[string]map[string]map[string]struct{}{},
			}
			md.(*metadata.DB).RegisterCollectibleResource(metadata.ResourceStream, sm)
			return sm, nil
		},
	})
}

type streamManager struct {
	// streams maps namespace -> name -> stream
	streams map[string]map[string]*managedStream

	byLease map[string]map[string]map[string]struct{}

	rwlock sync.RWMutex
}

func (sm *streamManager) Register(ctx context.Context, name string, stream streaming.Stream) error {
	ns, _ := namespaces.Namespace(ctx)
	ls, _ := leases.FromContext(ctx)

	ms := &managedStream{
		Stream:  stream,
		ns:      ns,
		name:    name,
		lease:   ls,
		manager: sm,
	}

	sm.rwlock.Lock()
	defer sm.rwlock.Unlock()
	nsMap, ok := sm.streams[ns]
	if !ok {
		nsMap = make(map[string]*managedStream)
		sm.streams[ns] = nsMap
	}
	if _, ok := nsMap[name]; ok {
		return errdefs.ErrAlreadyExists
	}
	nsMap[name] = ms

	if ls != "" {
		nsMap, ok := sm.byLease[ns]
		if !ok {
			nsMap = make(map[string]map[string]struct{})
			sm.byLease[ns] = nsMap
		}
		lsMap, ok := nsMap[ls]
		if !ok {
			lsMap = make(map[string]struct{})
			nsMap[ls] = lsMap
		}
		lsMap[name] = struct{}{}
	}
	return nil
}

func (sm *streamManager) Get(ctx context.Context, name string) (streaming.Stream, error) {
	ns, _ := namespaces.Namespace(ctx)
	sm.rwlock.RLock()
	defer sm.rwlock.RUnlock()

	nsMap, ok := sm.streams[ns]
	if !ok {
		return nil, errdefs.ErrNotFound
	}
	stream, ok := nsMap[name]
	if !ok {
		return nil, errdefs.ErrNotFound
	}

	return stream, nil
}

func (sm *streamManager) StartCollection(ctx context.Context) (metadata.CollectionContext, error) {
	// lock now and collection will unlock on cancel or finish
	sm.rwlock.Lock()

	return &collectionContext{
		manager: sm,
	}, nil
}

func (sm *streamManager) ReferenceLabel() string {
	return "stream"
}

type managedStream struct {
	streaming.Stream

	ns      string
	name    string
	lease   string
	manager *streamManager
}

func (m *managedStream) Close() error {
	m.manager.rwlock.Lock()
	if nsMap, ok := m.manager.streams[m.ns]; ok {
		delete(nsMap, m.name)
		if len(nsMap) == 0 {
			delete(m.manager.streams, m.ns)
		}
	}
	if m.lease != "" {
		if nsMap, ok := m.manager.byLease[m.ns]; ok {
			if lsMap, ok := nsMap[m.lease]; ok {
				delete(lsMap, m.name)
				if len(lsMap) == 0 {
					delete(nsMap, m.lease)
				}
			}
			if len(nsMap) == 0 {
				delete(m.manager.byLease, m.ns)
			}
		}
	}

	m.manager.rwlock.Unlock()
	return m.Stream.Close()
}

type collectionContext struct {
	manager *streamManager
	removed []gc.Node
}

func (cc *collectionContext) All(fn func(gc.Node)) {
	for ns, nsMap := range cc.manager.streams {
		for name := range nsMap {
			fn(gc.Node{
				Type:      metadata.ResourceStream,
				Namespace: ns,
				Key:       name,
			})
		}
	}

}

func (cc *collectionContext) Active(ns string, fn func(gc.Node)) {
	if nsMap, ok := cc.manager.streams[ns]; ok {
		for name, stream := range nsMap {
			// Don't consider leased streams as active, the lease
			// will determine the status
			// TODO: expire non-active streams
			if stream.lease == "" {
				fn(gc.Node{
					Type:      metadata.ResourceStream,
					Namespace: ns,
					Key:       name,
				})
			}
		}
	}
}

func (cc *collectionContext) Leased(ns, lease string, fn func(gc.Node)) {
	if nsMap, ok := cc.manager.byLease[ns]; ok {
		if lsMap, ok := nsMap[lease]; ok {
			for name := range lsMap {
				fn(gc.Node{
					Type:      metadata.ResourceStream,
					Namespace: ns,
					Key:       name,
				})
			}
		}
	}
}

func (cc *collectionContext) Remove(n gc.Node) {
	cc.removed = append(cc.removed, n)
}

func (cc *collectionContext) Cancel() error {
	cc.manager.rwlock.Unlock()
	return nil
}

func (cc *collectionContext) Finish() error {
	var closeStreams []streaming.Stream
	for _, node := range cc.removed {
		var lease string
		if nsMap, ok := cc.manager.streams[node.Namespace]; ok {
			if ms, ok := nsMap[node.Key]; ok {
				delete(nsMap, node.Key)
				closeStreams = append(closeStreams, ms.Stream)
				lease = ms.lease
			}
			if len(nsMap) == 0 {
				delete(cc.manager.streams, node.Namespace)
			}
		}
		if lease != "" {
			if nsMap, ok := cc.manager.byLease[node.Namespace]; ok {
				if lsMap, ok := nsMap[lease]; ok {
					delete(lsMap, node.Key)
					if len(lsMap) == 0 {
						delete(nsMap, lease)
					}
				}
				if len(nsMap) == 0 {
					delete(cc.manager.byLease, node.Namespace)
				}
			}
		}
	}
	cc.manager.rwlock.Unlock()

	var errs []error
	for _, s := range closeStreams {
		if err := s.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
