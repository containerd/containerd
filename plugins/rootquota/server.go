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

package rootquota

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/events/exchange"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/internal/cri/constants"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	"github.com/sirupsen/logrus"
)

type cache struct {
	rwlock sync.RWMutex
	index  map[string]string
}

type rootQuotaService struct {
	size           string
	mountPoint     string
	events         *exchange.Exchange
	snapshotter    snapshots.Snapshotter
	containerStore containers.Store
	datafile       string
	cache          cache
}

func (s *rootQuotaService) Run(pContext context.Context) {
	ctx, cancel := context.WithCancel(pContext)
	filters := []string{
		`topic=="/containers/create"`,
		`topic=="/containers/delete"`,
	}
	subscribeCh, errCh := s.events.Subscribe(ctx, filters...)
	go func() {
		if err := s.saveData(); err != nil {
			log.G(ctx).WithError(err).Errorf("Failed to saveData")
		}
	}()
	for {
		select {
		case err := <-errCh:
			if err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to handle event stream")
			}
		case e := <-subscribeCh:
			if e.Namespace != constants.K8sContainerdNamespace {
				log.G(ctx).Debugf("Ignoring events in namespace - %s", e.Namespace)
				break
			}
			evt, err := typeurl.UnmarshalAny(e.Event)
			if err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to convert event")
				break
			}
			if err := s.handleEvent(evt); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to handle event %+v", evt)
				// TODO Add an error retry queue
			}
		case <-ctx.Done():
			cancel()
			if err := s.saveData(); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to saveData")
			}
			return
		}

	}
}

func (s *rootQuotaService) handleEvent(any interface{}) error {
	namespace := namespaces.WithNamespace(context.Background(), constants.K8sContainerdNamespace)
	ctx, cancel := context.WithTimeout(namespace, 10*time.Second)
	defer cancel()
	switch e := any.(type) {
	case *eventstypes.ContainerCreate:
		logrus.Debugf("received ContainerCreate event %+v", e)
		c, err := s.containerStore.Get(ctx, e.ID)
		if err != nil {
			return fmt.Errorf("Failed get container by id %s err: %w", e.ID, err)
		}
		if v := c.Labels["io.cri-containerd.kind"]; v != "container" {
			return nil
		}
		if c.Snapshotter != "overlayfs" {
			return nil
		}
		mounts, err := s.snapshotter.Mounts(ctx, c.SnapshotKey)
		if err != nil {
			return fmt.Errorf("Failed get snapshot mounts by key %s err: %w", e.ID, err)
		}
		dir, projid := parseMountToProject(mounts)
		if dir == "" || projid == "" {
			return fmt.Errorf("not found snapshot id")
		}
		if err = s.createQuota(ctx, dir, projid, c.ID); err != nil {
			return fmt.Errorf("failed createQuota err: %w", err)
		}
	case *eventstypes.ContainerDelete:
		logrus.Debugf("received ContainerDelete event %+v", e)
		projid := s.getProjid(e.ID)
		if projid == "" {
			return nil
		}
		if err := s.deleteQuota(ctx, e.ID, projid); err != nil {
			return fmt.Errorf("failed deleteQuota err: %w", err)
		}
	default:
		return nil
	}
	return nil
}

func (s *rootQuotaService) reloadDatafile() (map[string]string, error) {
	readFile, err := os.ReadFile(s.datafile)
	if err != nil {
		return nil, fmt.Errorf("plugin read datafile err: %w", err)
	}
	index := make(map[string]string)
	//FIXME not support in windonws
	for _, line := range strings.Split(string(readFile), "\n") {
		if line == "" {
			continue
		}
		split := strings.Split(line, " ")
		index[split[0]] = split[1]
	}
	return index, nil
}

func (s *rootQuotaService) reloadContainers(ctx context.Context) error {
	ctxnamespace := namespaces.WithNamespace(ctx, constants.K8sContainerdNamespace)
	cntrs, err := s.containerStore.List(ctxnamespace, fmt.Sprintf("labels.%q==%q", "io.cri-containerd.kind", "container"))
	if err != nil {
		return err
	}
	for _, c := range cntrs {
		logrus.Debugf("containerStore list container item c %+v", c)
		if c.Snapshotter != "overlayfs" {
			continue
		}
		mounts, err := s.snapshotter.Mounts(ctxnamespace, c.SnapshotKey)
		if err != nil {
			if strings.HasSuffix(err.Error(), "not found") {
				continue
			}
			return fmt.Errorf("Failed get snapshot mounts by key %s err: %w", c.SnapshotKey, err)
		}
		dir, projid := parseMountToProject(mounts)
		if dir == "" || projid == "" {
			return fmt.Errorf("not found snapshot id")
		}
		s.createQuota(ctxnamespace, dir, projid, c.ID)
	}
	return nil
}

func parseMountToProject(mounts []mount.Mount) (dir, projid string) {
	for _, m := range mounts {
		if m.Type == "overlay" && m.Source == "overlay" {
			for _, option := range m.Options {
				if strings.HasPrefix(option, "upperdir") {
					dir = option[9 : len(option)-3]
					for i := len(dir) - 1; i > 0; i-- {
						if dir[i] == filepath.Separator {
							projid = dir[i+1:]
							break
						}
					}
					break
				}
			}
		}
	}
	return dir, projid
}

func (s *rootQuotaService) createQuota(ctx context.Context, projdir, projid, containerID string) (resE error) {
	projcmd := exec.CommandContext(ctx, "xfs_quota", "-x", "-c", fmt.Sprintf("project -s -p %s %s", projdir, projid), s.mountPoint)
	if err := projcmd.Run(); err != nil {
		return err
	}
	defer func() {
		if resE != nil {
			limitCleanCmd := exec.Command("xfs_quota", "-x", "-c", fmt.Sprintf("limit -p bhard=0 %s", projid), s.mountPoint)
			if err := limitCleanCmd.Run(); err != nil {
				logrus.Errorf("limitCleanCmd err: %s", err)
			}
		}
	}()
	limitCmd := exec.Command("xfs_quota", "-x", "-c", fmt.Sprintf("limit -p bhard=%s %s", s.size, projid), s.mountPoint)
	if err := limitCmd.Run(); err != nil {
		return err
	}
	s.updateCache(containerID, projid)
	logrus.Debugf("createQuota success projdir: %s, projid: %s, containerID: %s", projdir, projid, containerID)
	return nil
}

func (s *rootQuotaService) deleteQuota(ctx context.Context, containerID string, projid string) (resE error) {
	limitCmd := exec.CommandContext(ctx, "xfs_quota", "-x", "-c", fmt.Sprintf("limit -p bhard=0 %s", projid), s.mountPoint)
	defer func() {
		if resE != nil {
			s.updateCache(containerID, projid)
		}
	}()
	if err := limitCmd.Run(); err != nil {
		return resE
	}
	s.deleteCache(containerID)
	logrus.Debugf("deleteQuota success projid: %s, containerID: %s", projid, containerID)
	return nil
}

func shortcutCntrID(containerID string) string {
	return containerID[:13]
}

// key is containerID, value is projid
func (s *rootQuotaService) updateCache(key, value string) {
	id := shortcutCntrID(key)
	s.cache.rwlock.Lock()
	defer s.cache.rwlock.Unlock()
	s.cache.index[id] = value
}

// key is containerID, value is projid
func (s *rootQuotaService) deleteCache(key string) {
	id := shortcutCntrID(key)
	s.cache.rwlock.Lock()
	defer s.cache.rwlock.Unlock()
	delete(s.cache.index, id)
}

func (s *rootQuotaService) saveData() error {
	s.cache.rwlock.Lock()
	defer s.cache.rwlock.Unlock()
	buffer := bytes.Buffer{}
	buffer.Grow(20 * len(s.cache.index))
	for key, value := range s.cache.index {
		buffer.WriteString(key)
		buffer.WriteString(" ")
		buffer.WriteString(value)
		buffer.WriteString("\n")
	}
	file, err := os.OpenFile(s.datafile, os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err = file.WriteString(buffer.String()); err != nil {
		return err
	}
	return nil
}

func (s *rootQuotaService) getProjid(key string) string {
	id := shortcutCntrID(key)
	s.cache.rwlock.RLock()
	defer s.cache.rwlock.RUnlock()
	return s.cache.index[id]
}

func (s *rootQuotaService) deleteQuotaWhenInit(ctx context.Context, containerID string, projid string) (resE error) {
	limitCmd := exec.CommandContext(ctx, "xfs_quota", "-x", "-c", fmt.Sprintf("limit -p bhard=0 %s", projid), s.mountPoint)
	if err := limitCmd.Run(); err != nil {
		return resE
	}
	return nil
}
