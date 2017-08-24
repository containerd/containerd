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

import "io"

// writeCloseInformer wraps passed in write closer with a close channel.
// Caller could wait on the close channel for the write closer to be
// closed.
type writeCloseInformer struct {
	close chan struct{}
	wc    io.WriteCloser
}

// NewWriteCloseInformer creates the writeCloseInformer from a write closer.
func NewWriteCloseInformer(wc io.WriteCloser) (io.WriteCloser, <-chan struct{}) {
	close := make(chan struct{})
	return &writeCloseInformer{
		close: close,
		wc:    wc,
	}, close
}

// Write passes through the data into the internal write closer.
func (w *writeCloseInformer) Write(p []byte) (int, error) {
	return w.wc.Write(p)
}

// Close closes the internal write closer and inform the close channel.
func (w *writeCloseInformer) Close() error {
	err := w.wc.Close()
	close(w.close)
	return err
}

// nopWriteCloser wraps passed in writer with a nop close function.
type nopWriteCloser struct {
	w io.Writer
}

// NewNopWriteCloser creates the nopWriteCloser from a writer.
func NewNopWriteCloser(w io.Writer) io.WriteCloser {
	return &nopWriteCloser{w: w}
}

// Write passes through the data into the internal writer.
func (n *nopWriteCloser) Write(p []byte) (int, error) {
	return n.w.Write(p)
}

// Close is a nop close function.
func (n *nopWriteCloser) Close() error {
	return nil
}
