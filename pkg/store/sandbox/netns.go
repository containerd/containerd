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
	"fmt"
	"sync"

	cnins "github.com/containernetworking/plugins/pkg/ns"
)

// NetNS holds network namespace for sandbox
type NetNS struct {
	sync.Mutex
	ns     cnins.NetNS
	closed bool
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

// Remove removes network namepace if it exists and not closed. Remove is idempotent,
// meaning it might be invoked multiple times and provides consistent result.
func (n *NetNS) Remove() error {
	n.Lock()
	defer n.Unlock()
	if !n.closed {
		err := n.ns.Close()
		if err != nil {
			return err
		}
		n.closed = true
	}
	return nil
}

// GetPath returns network namespace path for sandbox container
func (n *NetNS) GetPath() string {
	n.Lock()
	defer n.Unlock()
	return n.ns.Path()
}
