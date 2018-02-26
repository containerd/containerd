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

package store

import "sync"

// StopCh is used to propagate the stop information of a container.
type StopCh struct {
	mu     sync.Mutex
	ch     chan struct{}
	closed bool
}

// NewStopCh creates a stop channel. The channel is open by default.
func NewStopCh() *StopCh {
	return &StopCh{
		ch:     make(chan struct{}),
		closed: false,
	}
}

// Stop close stopCh of the container.
func (s *StopCh) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	close(s.ch)
	s.closed = true
}

// Stopped return the stopCh of the container as a readonly channel.
func (s *StopCh) Stopped() <-chan struct{} {
	return s.ch
}
