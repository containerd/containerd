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

package process

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

type File struct {
	*os.File
	mu     sync.Mutex
	path   string
	sighup chan os.Signal
}

func (lr *File) reopen() (err error) {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	lr.File.Close()
	lr.File, err = os.OpenFile(lr.path, syscall.O_WRONLY|syscall.O_APPEND|os.O_CREATE, 0644)
	return
}

func OpenFile(path string) (*File, error) {
	lr := &File{
		mu:     sync.Mutex{},
		path:   path,
		sighup: make(chan os.Signal, 1),
	}

	if err := lr.reopen(); err != nil {
		return nil, err
	}

	go func() {
		signal.Notify(lr.sighup, syscall.SIGSEGV)
		for range lr.sighup {
			if err := lr.reopen(); err != nil {
				fmt.Fprintf(os.Stderr, "Error reopening: %s\n", err)
			}
		}
	}()
	return lr, nil
}

func (lr *File) Write(b []byte) (int, error) {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	return lr.File.Write(b)
}

func (lr *File) Close() error {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	signal.Stop(lr.sighup)
	close(lr.sighup)
	return lr.File.Close()
}
