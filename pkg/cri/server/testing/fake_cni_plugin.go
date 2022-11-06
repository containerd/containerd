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

package testing

import (
	"context"

	cni "github.com/containerd/go-cni"
)

// FakeCNIPlugin is a fake plugin used for test.
type FakeCNIPlugin struct {
	StatusErr error
	LoadErr   error
}

// NewFakeCNIPlugin create a FakeCNIPlugin.
func NewFakeCNIPlugin() *FakeCNIPlugin {
	return &FakeCNIPlugin{}
}

// Setup setups the network of PodSandbox.
func (f *FakeCNIPlugin) Setup(ctx context.Context, id, path string, opts ...cni.NamespaceOpts) (*cni.Result, error) {
	return nil, nil
}

// SetupSerially sets up the network of PodSandbox without doing the interfaces in parallel.
func (f *FakeCNIPlugin) SetupSerially(ctx context.Context, id, path string, opts ...cni.NamespaceOpts) (*cni.Result, error) {
	return nil, nil
}

// Remove teardown the network of PodSandbox.
func (f *FakeCNIPlugin) Remove(ctx context.Context, id, path string, opts ...cni.NamespaceOpts) error {
	return nil
}

// Check the network of PodSandbox.
func (f *FakeCNIPlugin) Check(ctx context.Context, id, path string, opts ...cni.NamespaceOpts) error {
	return nil
}

// Status get the status of the plugin.
func (f *FakeCNIPlugin) Status() error {
	return f.StatusErr
}

// Load loads the network config.
func (f *FakeCNIPlugin) Load(opts ...cni.Opt) error {
	return f.LoadErr
}

// GetConfig returns a copy of the CNI plugin configurations as parsed by CNI
func (f *FakeCNIPlugin) GetConfig() *cni.ConfigResult {
	return nil
}
