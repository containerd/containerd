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
	sync.RWMutex

	// plugins is used to setup and teardown network when run/stop pod sandbox.
	plugins map[string]cni.CNI
	// confMonitor is used to reload cni network conf if there is
	// any valid fs change events from cni network conf dir.
	confMonitor map[string]*cniNetConfSyncer
	// confSyncWG tracks running conf syncers.
	confSyncWG sync.WaitGroup
	// confSyncErrCh reports errors from CNI conf syncers. This channel is initialized
	// when `start()` is called and closed when all syncers have exited.
	confSyncErrCh chan error
	// started indicates if `start()` has been called.
	started bool
	// confSyncWaitCreated indicates if we've created a goroutine to wait for
	// all conf sync stop.
	confSyncWaitCreated bool
}

func newCNINetPlugin() *cniNetPlugin {
	return &cniNetPlugin{
		plugins:     make(map[string]cni.CNI),
		confMonitor: make(map[string]*cniNetConfSyncer),
	}
}

// add adds a new CNI plugin and creates conf syncer for it.
func (c *cniNetPlugin) add(name, confDir string, i cni.CNI, loadOpts []cni.Opt) error {
	c.Lock()
	defer c.Unlock()
	if _, ok := c.plugins[name]; ok {
		return fmt.Errorf("CNI plugin %s already exists", name)
	}

	c.plugins[name] = i
	if confDir != "" {
		m, err := newCNINetConfSyncer(confDir, i, loadOpts)
		if err != nil {
			return fmt.Errorf("failed to create cni conf monitor for %s: %w", name, err)
		}
		c.confMonitor[name] = m
		// If `start()` has been called, we need to start the syncloop directly.
		if c.started {
			log.L.Infof("Start cni network conf monitor for %s", name)
			c.confSyncWG.Go(func() {
				c.confSyncErrCh <- m.syncLoop()
			})
		}
	}
	c.waitForConfSyncStop()
	return nil
}

// get returns the CNI plugin associated with `name`.
func (c *cniNetPlugin) get(name string) cni.CNI {
	c.RLock()
	defer c.RUnlock()
	if netPlugin, ok := c.plugins[name]; ok {
		return netPlugin
	}
	return nil
}

// start starts all CNI conf syncers and returns a channel to report conf sync errors.
// If a new CNI is added after `start()`, its syncer will start automatically.
func (c *cniNetPlugin) start() <-chan error {
	c.Lock()
	defer c.Unlock()
	if !c.started {
		c.started = true
		c.confSyncErrCh = make(chan error, len(c.confMonitor))
		for name, syncer := range c.confMonitor {
			log.L.Infof("Start cni network conf monitor for %s", name)
			c.confSyncWG.Go(func() {
				c.confSyncErrCh <- syncer.syncLoop()
			})
		}
		c.waitForConfSyncStop()
	}
	return c.confSyncErrCh
}

// close closes all CNI conf syncers.
func (c *cniNetPlugin) close() error {
	var errs []error
	c.Lock()
	defer c.Unlock()
	for name, syncer := range c.confMonitor {
		if err := syncer.stop(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close cni conf monitor for %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// status returns CNI load statuses for all CNI plugins.
// This is used in a verbose CRI `Status` call.
func (c *cniNetPlugin) status() map[string]string {
	c.RLock()
	defer c.RUnlock()
	res := make(map[string]string, len(c.confMonitor)+1)

	defaultStatus := "OK"
	for name, h := range c.confMonitor {
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

func (c *cniNetPlugin) waitForConfSyncStop() {
	// For platforms that may not support CNI (darwin etc.) there's no
	// use in launching this as `Wait` will return immediately. Further
	// down we select on this channel along with some others to determine
	// if we should Close() the CRI service, so closing this preemptively
	// isn't good.
	if c.started && len(c.confMonitor) > 0 && !c.confSyncWaitCreated {
		c.confSyncWaitCreated = true
		go func() {
			for err := range c.confSyncErrCh {
				if err != nil {
					log.L.Errorf("CNI conf syncer error: %v", err)
				}
			}
		}()
		go func() {
			c.confSyncWG.Wait()
			close(c.confSyncErrCh)
		}()
	}
}
