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

package nri

import (
	"context"
	"fmt"
	"sync"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	nri "github.com/containerd/nri/pkg/adaptation"
	"github.com/sirupsen/logrus"
)

// Domain implements the functions the generic NRI interface needs to
// deal with pods and containers from a particular containerd namespace.
type Domain interface {
	// GetName returns the containerd namespace for this domain.
	GetName() string

	// ListPodSandboxes lists all pods in this namespace.
	ListPodSandboxes() []PodSandbox

	// ListContainers lists all containers in this namespace.
	ListContainers() []Container

	// GetPodSandbox returns the pod for the given ID.
	GetPodSandbox(string) (PodSandbox, bool)

	// GetContainer returns the container for the given ID.
	GetContainer(string) (Container, bool)

	// UpdateContainer applies an NRI container update request in the namespace.
	UpdateContainer(context.Context, *nri.ContainerUpdate) error

	// EvictContainer evicts the requested container in the namespace.
	EvictContainer(context.Context, *nri.ContainerEviction) error
}

// RegisterDomain registers an NRI domain for a containerd namespace.
func RegisterDomain(d Domain) {
	err := domains.add(d)
	if err != nil {
		logrus.WithError(err).Fatalf("Failed to register namespace %q with NRI", d.GetName())
	}

	logrus.Infof("Registered namespace %q with NRI", d.GetName())
}

type domainTable struct {
	sync.Mutex
	domains map[string]Domain
}

func (t *domainTable) add(d Domain) error {
	t.Lock()
	defer t.Unlock()

	namespace := d.GetName()

	if _, ok := t.domains[namespace]; ok {
		return errdefs.ErrAlreadyExists
	}

	t.domains[namespace] = d
	return nil
}

func (t *domainTable) listPodSandboxes() []PodSandbox {
	var pods []PodSandbox

	t.Lock()
	defer t.Unlock()

	for _, d := range t.domains {
		pods = append(pods, d.ListPodSandboxes()...)
	}
	return pods
}

func (t *domainTable) listContainers() []Container {
	var ctrs []Container

	t.Lock()
	defer t.Unlock()

	for _, d := range t.domains {
		ctrs = append(ctrs, d.ListContainers()...)
	}
	return ctrs
}

func (t *domainTable) getContainer(id string) (Container, Domain) {
	t.Lock()
	defer t.Unlock()

	// TODO(klihub): Are ID conflicts across namespaces possible ? Probably...

	for _, d := range t.domains {
		if ctr, ok := d.GetContainer(id); ok {
			return ctr, d
		}
	}
	return nil, nil
}

func (t *domainTable) updateContainers(ctx context.Context, updates []*nri.ContainerUpdate) ([]*nri.ContainerUpdate, error) {
	var failed []*nri.ContainerUpdate

	for _, u := range updates {
		_, d := t.getContainer(u.ContainerId)
		if d == nil {
			continue
		}

		domain := d.GetName()
		err := d.UpdateContainer(namespaces.WithNamespace(ctx, domain), u)
		if err != nil {
			log.G(ctx).WithError(err).Errorf("NRI update of %s container %s failed",
				domain, u.ContainerId)
			if !u.IgnoreFailure {
				failed = append(failed, u)
			}
			continue
		}

		log.G(ctx).Tracef("NRI update of %s container %s successful", domain, u.ContainerId)
	}

	if len(failed) != 0 {
		return failed, fmt.Errorf("NRI update of some containers failed")
	}

	return nil, nil
}

func (t *domainTable) evictContainers(ctx context.Context, evict []*nri.ContainerEviction) ([]*nri.ContainerEviction, error) {
	var failed []*nri.ContainerEviction

	for _, e := range evict {
		_, d := t.getContainer(e.ContainerId)
		if d == nil {
			continue
		}

		domain := d.GetName()
		err := d.EvictContainer(namespaces.WithNamespace(ctx, domain), e)
		if err != nil {
			log.G(ctx).WithError(err).Errorf("NRI eviction of %s container %s failed",
				domain, e.ContainerId)
			failed = append(failed, e)
			continue
		}

		log.G(ctx).Tracef("NRI eviction of %s container %s successful", domain, e.ContainerId)
	}

	if len(failed) != 0 {
		return failed, fmt.Errorf("NRI eviction of some containers failed")
	}

	return nil, nil
}

var domains = &domainTable{
	domains: make(map[string]Domain),
}
