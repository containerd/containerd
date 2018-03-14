/*
Copyright 2017 The Kubernetes Authors.

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

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"sync"

	cnins "github.com/containernetworking/plugins/pkg/ns"
	"github.com/docker/docker/pkg/mount"
	"github.com/docker/docker/pkg/symlink"
	"golang.org/x/sys/unix"
)

// ErrClosedNetNS is the error returned when network namespace is closed.
var ErrClosedNetNS = errors.New("network namespace is closed")

// NetNS holds network namespace for sandbox
type NetNS struct {
	sync.Mutex
	ns       cnins.NetNS
	closed   bool
	restored bool
}

// NewNetNS creates a network namespace for the sandbox
func NewNetNS() (*NetNS, error) {
	netns, err := cnins.NewNS()
	if err != nil {
		return nil, fmt.Errorf("failed to setup network namespace %v", err)
	}
	n := new(NetNS)
	n.ns = netns
	return n, nil
}

// LoadNetNS loads existing network namespace. It returns ErrClosedNetNS
// if the network namespace has already been closed.
func LoadNetNS(path string) (*NetNS, error) {
	ns, err := cnins.GetNS(path)
	if err != nil {
		if _, ok := err.(cnins.NSPathNotExistErr); ok {
			return nil, ErrClosedNetNS
		}
		if _, ok := err.(cnins.NSPathNotNSErr); ok {
			// Do best effort cleanup.
			os.RemoveAll(path) // nolint: errcheck
			return nil, ErrClosedNetNS
		}
		return nil, fmt.Errorf("failed to load network namespace %v", err)
	}
	return &NetNS{ns: ns, restored: true}, nil
}

// Remove removes network namepace if it exists and not closed. Remove is idempotent,
// meaning it might be invoked multiple times and provides consistent result.
func (n *NetNS) Remove() error {
	n.Lock()
	defer n.Unlock()
	if !n.closed {
		err := n.ns.Close()
		if err != nil {
			return fmt.Errorf("failed to close network namespace: %v", err)
		}
		n.closed = true
	}
	if n.restored {
		path := n.ns.Path()
		// TODO(random-liu): Add util function for unmount.
		// Check netns existence.
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("failed to stat netns: %v", err)
		}
		path, err := symlink.FollowSymlinkInScope(path, "/")
		if err != nil {
			return fmt.Errorf("failed to follow symlink: %v", err)
		}
		mounted, err := mount.Mounted(path)
		if err != nil {
			return fmt.Errorf("failed to check netns mounted: %v", err)
		}
		if mounted {
			err := unix.Unmount(path, unix.MNT_DETACH)
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("failed to umount netns: %v", err)
			}
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("failed to remove netns: %v", err)
		}
		n.restored = false
	}
	return nil
}

// Closed checks whether the network namespace has been closed.
func (n *NetNS) Closed() bool {
	n.Lock()
	defer n.Unlock()
	return n.closed && !n.restored
}

// GetPath returns network namespace path for sandbox container
func (n *NetNS) GetPath() string {
	n.Lock()
	defer n.Unlock()
	return n.ns.Path()
}
