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

package util

import (
	"context"
	"os"
	"path/filepath"

	"github.com/containerd/log"
	"github.com/fsnotify/fsnotify"
)

type NotifyEventFunc func(context.Context, fsnotify.Event) error

type NotifyFile struct {
	watch *fsnotify.Watcher
}

func NewNotifyFile() (*NotifyFile, error) {
	w := new(NotifyFile)
	watch, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w.watch = watch
	return w, nil
}

func (nf *NotifyFile) WatchFile(ctx context.Context, file string, fc NotifyEventFunc) error {
	dir, _ := filepath.Split(file)
	err := nf.watch.Add(dir)
	go nf.watchEvent(ctx, fc)
	return err
}

func (nf *NotifyFile) WatchDir(ctx context.Context, dir string, fc NotifyEventFunc) error {
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			path, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			err = nf.watch.Add(path)
			if err != nil {
				return err
			}
		}
		return nil
	})
	go nf.watchEvent(ctx, fc)
	return err
}

func (nf *NotifyFile) watchEvent(ctx context.Context, fc NotifyEventFunc) {
	for {
		select {
		case <-ctx.Done():
			log.G(ctx).Infof("exit fsnotify, recive ctx done event")
			return
		case ev := <-nf.watch.Events:
			if ev.Op&fsnotify.Create == fsnotify.Create {
				file, err := os.Stat(ev.Name)
				if err == nil && file.IsDir() {
					nf.watch.Add(ev.Name)
					log.G(ctx).Infof("event type is create dir, add watch: %s", ev.Name)
				}
			}

			if ev.Op&fsnotify.Remove == fsnotify.Remove {
				fi, err := os.Stat(ev.Name)
				if err == nil && fi.IsDir() {
					nf.watch.Remove(ev.Name)
					log.G(ctx).Infof("event type is delete dir, remove watch: %s", ev.Name)
				}
			}

			if ev.Op&fsnotify.Rename == fsnotify.Rename {
				nf.watch.Remove(ev.Name)
				log.G(ctx).Infof("event type is rename, remove this watch: %s", ev.Name)
			}
			// filter swp file
			_, fileName := filepath.Split(ev.Name)
			if fileName[0] == '.' {
				break
			}
			if err := fc(ctx, ev); err != nil {
				log.G(ctx).Errorf("call NotifyEventFunc function error:%s", err)
			}
		case err := <-nf.watch.Errors:
			log.G(ctx).Errorf("watch event error: %+v", err)
			return
		}
	}
}
