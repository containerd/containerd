//go:build linux

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

package loop

import (
	"context"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/log"
	"os"
	"path/filepath"
)

type Driver interface {
	Prepare(ctx context.Context, src string) (string, error)
	Remove(ctx context.Context, path string) error
	Get(ctx context.Context, path string) (string, error)
}

type defaultDriver struct {
}

func NewLoopDriver() (Driver, error) {
	return &defaultDriver{}, nil
}

//TODO(chaofeng): Store loopName in db instead of a file

// Prepare creates a loop device for the given source.
func (u *defaultDriver) Prepare(ctx context.Context, src string) (string, error) {
	loopName, err := mount.AttachLoopDevice(src)
	log.G(ctx).Infof("loop device  %v created for: %s", loopName, src)
	if err != nil {
		log.G(ctx).Infof("loop device  %v created failed for: %s %v", loopName, src, err)
		return "", err
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(src), "loop"), []byte(loopName), 0644); err != nil {
		return "", err
	}
	return loopName, nil
}

func (u *defaultDriver) Remove(ctx context.Context, path string) error {
	str, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	log.G(ctx).Infof("loop device detached for: %s", str)
	return mount.DetachLoopDevice(string(str))
}

func (u *defaultDriver) Get(ctx context.Context, path string) (string, error) {
	str, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(str), nil
}
