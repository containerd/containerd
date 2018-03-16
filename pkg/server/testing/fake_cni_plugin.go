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

package testing

import (
	cni "github.com/containerd/go-cni"
)

// FakeCNIPlugin is a fake plugin used for test.
type FakeCNIPlugin struct{}

// NewFakeCNIPlugin create a FakeCNIPlugin.
func NewFakeCNIPlugin() *FakeCNIPlugin {
	return &FakeCNIPlugin{}
}

// Setup setups the network of PodSandbox.
func (f *FakeCNIPlugin) Setup(id, path string, opts ...cni.NamespaceOpts) (*cni.CNIResult, error) {
	return nil, nil
}

// Remove teardown the network of PodSandbox.
func (f *FakeCNIPlugin) Remove(id, path string, opts ...cni.NamespaceOpts) error {
	return nil
}

// Status get the status of the plugin.
func (f *FakeCNIPlugin) Status() error {
	return nil
}

// Load loads the network config.
func (f *FakeCNIPlugin) Load(opts ...cni.LoadOption) error {
	return nil
}
