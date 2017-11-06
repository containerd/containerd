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

package ioutil

import (
	"io"
	"sync"
)

// writeCloseInformer wraps a reader with a close function.
type wrapReadCloser struct {
	// TODO(random-liu): Evaluate whether the lock introduces
	// performance regression.
	sync.RWMutex
	r      io.Reader
	closed bool
}

// NewWrapReadCloser creates a wrapReadCloser from a reader.
func NewWrapReadCloser(r io.Reader) io.ReadCloser {
	return &wrapReadCloser{r: r}
}

// Read reads up to len(p) bytes into p.
func (w *wrapReadCloser) Read(p []byte) (int, error) {
	w.RLock()
	defer w.RUnlock()
	if w.closed {
		return 0, io.EOF
	}
	return w.r.Read(p)
}

// Close closes read closer.
func (w *wrapReadCloser) Close() error {
	w.Lock()
	defer w.Unlock()
	w.closed = true
	return nil
}
