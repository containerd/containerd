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

package container

import (
	"sync"

	"github.com/containerd/containerd"
)

// Client holds the containerd container client.
// containerd.Container is a pointer underlying. New assignment won't affect
// the previous pointer, so simply lock around is enough.
type Client struct {
	lock      sync.RWMutex
	container containerd.Container
}

// Get containerd container client.
func (c *Client) Get() containerd.Container {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.container
}

// Set containerd container client.
func (c *Client) Set(container containerd.Container) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.container = container
}
