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
	"fmt"
	"os"
	"path/filepath"
	"sync"

	cni "github.com/containerd/go-cni"
	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

// cniNetConfSyncer is used to reload cni network conf triggered by fs change
// events.
type cniNetConfSyncer struct {
	// only used for lastSyncStatus
	sync.RWMutex
	lastSyncStatus error

	watcher   *fsnotify.Watcher
	confDir   string
	netPlugin cni.CNI
	loadOpts  []cni.Opt
}

// newCNINetConfSyncer creates cni network conf syncer.
func newCNINetConfSyncer(confDir string, netPlugin cni.CNI, loadOpts []cni.Opt) (*cniNetConfSyncer, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}

	// /etc/cni has to be readable for non-root users (0755), because /etc/cni/tuning/allowlist.conf is used for rootless mode too.
	// This file was introduced in CNI plugins 1.2.0 (https://github.com/containernetworking/plugins/pull/693), and its path is hard-coded.
	confDirParent := filepath.Dir(confDir)
	if err := os.MkdirAll(confDirParent, 0755); err != nil {
		return nil, fmt.Errorf("failed to create the parent of the cni conf dir=%s: %w", confDirParent, err)
	}

	if err := os.MkdirAll(confDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create cni conf dir=%s for watch: %w", confDir, err)
	}

	if err := watcher.Add(confDir); err != nil {
		return nil, fmt.Errorf("failed to watch cni conf dir %s: %w", confDir, err)
	}

	syncer := &cniNetConfSyncer{
		watcher:   watcher,
		confDir:   confDir,
		netPlugin: netPlugin,
		loadOpts:  loadOpts,
	}

	if err := syncer.netPlugin.Load(syncer.loadOpts...); err != nil {
		logrus.WithError(err).Error("failed to load cni during init, please check CRI plugin status before setting up network for pods")
		syncer.updateLastStatus(err)
	}
	return syncer, nil
}

// syncLoop monitors any fs change events from cni conf dir and tries to reload
// cni configuration.
func (syncer *cniNetConfSyncer) syncLoop() error {
	for {
		select {
		case event, ok := <-syncer.watcher.Events:
			if !ok {
				logrus.Debugf("cni watcher channel is closed")
				return nil
			}
			// Only reload config when receiving write/rename/remove
			// events
			//
			// TODO(fuweid): Might only reload target cni config
			// files to prevent no-ops.
			if event.Has(fsnotify.Chmod) || event.Has(fsnotify.Create) {
				logrus.Debugf("ignore event from cni conf dir: %s", event)
				continue
			}
			logrus.Debugf("receiving change event from cni conf dir: %s", event)

			lerr := syncer.netPlugin.Load(syncer.loadOpts...)
			if lerr != nil {
				logrus.WithError(lerr).
					Errorf("failed to reload cni configuration after receiving fs change event(%s)", event)
			}
			syncer.updateLastStatus(lerr)

		case err := <-syncer.watcher.Errors:
			if err != nil {
				logrus.WithError(err).Error("failed to continue sync cni conf change")
				return err
			}
		}
	}
}

// lastStatus retrieves last sync status.
func (syncer *cniNetConfSyncer) lastStatus() error {
	syncer.RLock()
	defer syncer.RUnlock()
	return syncer.lastSyncStatus
}

// updateLastStatus will be called after every single cni load.
func (syncer *cniNetConfSyncer) updateLastStatus(err error) {
	syncer.Lock()
	defer syncer.Unlock()
	syncer.lastSyncStatus = err
}

// stop stops watcher in the syncLoop.
func (syncer *cniNetConfSyncer) stop() error {
	return syncer.watcher.Close()
}
