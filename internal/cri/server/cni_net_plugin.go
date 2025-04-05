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

package server

import (
	"errors"
	"fmt"
	"sync"

	"github.com/containerd/go-cni"
	"github.com/containerd/log"
)

type cniNetPlugin struct {
	netSyncGroup sync.WaitGroup

	sync.RWMutex
	// netPlugin is used to setup and teardown network when run/stop pod sandbox.
	netPlugin map[string]cni.CNI
	// cniNetConfMonitor is used to reload cni network conf if there is
	// any valid fs change events from cni network conf dir.
	cniNetConfMonitor map[string]*cniNetConfSyncer
	// started indicates if `start()` has been called.
	started bool
	// errCh reports errors from CNI conf syncers. This channel is initialized
	// when `start()` is called and closed when all syncers have exited.
	errCh chan error
}

func (c *cniNetPlugin) addCNIPlugin(name, confDir string, opts []cni.Opt, loadOpts []cni.Opt) error {
	i, err := cni.New(opts...)
	if err != nil {
		return fmt.Errorf("failed to initialize cni plugin: %w", err)
	}

	m, err := newCNINetConfSyncer(confDir, i, loadOpts)
	if err != nil {
		return fmt.Errorf("failed to create cni conf monitor for %s: %w", name, err)
	}

	c.Lock()
	defer c.Unlock()

	if c.started {
		// If `start()` has been called, we need to start the sync loop directly
		// here, and if it replaces an existing syncer, we also stop the old one.
		c.netSyncGroup.Add(1)
		go func() {
			defer c.netSyncGroup.Done()
			c.errCh <- c.cniNetConfMonitor[name].syncLoop()
		}()

		if oldSyncer, ok := c.cniNetConfMonitor[name]; ok {
			if err := oldSyncer.stop(); err != nil {
				log.L.Errorf("failed to stop cni conf monitor for %s: %v", name, err)
			}
		}
	}

	c.netPlugin[name] = i
	c.cniNetConfMonitor[name] = m
	return nil
}

func (c *cniNetPlugin) get(name string) cni.CNI {
	c.RLock()
	defer c.RUnlock()
	if netPlugin, ok := c.netPlugin[name]; ok {
		return netPlugin
	}
	return nil
}

func (c *cniNetPlugin) start() <-chan error {
	c.Lock()
	defer c.Unlock()
	c.started = true
	c.errCh = make(chan error, len(c.cniNetConfMonitor))

	for name, syncer := range c.cniNetConfMonitor {
		log.L.Infof("Start cni network conf syncer for %s", name)
		c.netSyncGroup.Add(1)
		go func() {
			defer c.netSyncGroup.Done()
			c.errCh <- syncer.syncLoop()
		}()
	}

	// For platforms that may not support CNI (darwin etc.) there's no
	// use in launching this as `Wait` will return immediately. Further
	// down we select on this channel along with some others to determine
	// if we should Close() the CRI service, so closing this preemptively
	// isn't good.
	if len(c.cniNetConfMonitor) > 0 {
		go func() {
			c.netSyncGroup.Wait()
			close(c.errCh)
		}()
	}

	return c.errCh
}

func (c *cniNetPlugin) close() error {
	var errs []error
	c.RLock()
	defer c.RUnlock()
	for name, syncer := range c.cniNetConfMonitor {
		if err := syncer.stop(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close cni conf monitor for %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func (c *cniNetPlugin) status() map[string]string {
	c.RLock()
	defer c.RUnlock()
	res := make(map[string]string, len(c.cniNetConfMonitor)+1)

	defaultStatus := "OK"
	for name, h := range c.cniNetConfMonitor {
		s := "OK"
		if h == nil {
			continue
		}
		if lerr := h.lastStatus(); lerr != nil {
			s = lerr.Error()
		}
		res[fmt.Sprintf("lastCNILoadStatus.%s", name)] = s
		if name == defaultNetworkPlugin {
			defaultStatus = s
		}
	}
	res["lastCNILoadStatus"] = defaultStatus
	return res
}
