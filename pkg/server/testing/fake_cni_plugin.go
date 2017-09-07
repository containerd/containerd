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
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"github.com/cri-o/ocicni/pkg/ocicni"
)

// CalledDetail is the struct contains called function name and arguments.
type CalledDetail struct {
	// Name of the function called.
	Name string
	// Argument of the function called.
	Argument interface{}
}

// FakeCNIPlugin is a fake plugin used for test.
type FakeCNIPlugin struct {
	sync.Mutex
	called []CalledDetail
	errors map[string]error
	IPMap  map[string]string
}

// getError get error for call
func (f *FakeCNIPlugin) getError(op string) error {
	err, ok := f.errors[op]
	if ok {
		delete(f.errors, op)
		return err
	}
	return nil
}

// InjectError inject error for call
func (f *FakeCNIPlugin) InjectError(fn string, err error) {
	f.Lock()
	defer f.Unlock()
	f.errors[fn] = err
}

// InjectErrors inject errors for calls
func (f *FakeCNIPlugin) InjectErrors(errs map[string]error) {
	f.Lock()
	defer f.Unlock()
	for fn, err := range errs {
		f.errors[fn] = err
	}
}

// ClearErrors clear errors for call
func (f *FakeCNIPlugin) ClearErrors() {
	f.Lock()
	defer f.Unlock()
	f.errors = make(map[string]error)
}

func (f *FakeCNIPlugin) appendCalled(name string, argument interface{}) {
	call := CalledDetail{Name: name, Argument: argument}
	f.called = append(f.called, call)
}

// GetCalledNames get names of call
func (f *FakeCNIPlugin) GetCalledNames() []string {
	f.Lock()
	defer f.Unlock()
	names := []string{}
	for _, detail := range f.called {
		names = append(names, detail.Name)
	}
	return names
}

// GetCalledDetails get detail of each call.
func (f *FakeCNIPlugin) GetCalledDetails() []CalledDetail {
	f.Lock()
	defer f.Unlock()
	// Copy the list and return.
	return append([]CalledDetail{}, f.called...)
}

// SetFakePodNetwork sets the given IP for given arguments.
func (f *FakeCNIPlugin) SetFakePodNetwork(podNetwork ocicni.PodNetwork, ip string) {
	f.Lock()
	defer f.Unlock()
	f.IPMap[podNetwork.NetNS] = ip
}

// NewFakeCNIPlugin create a FakeCNIPlugin.
func NewFakeCNIPlugin() ocicni.CNIPlugin {
	return &FakeCNIPlugin{
		errors: make(map[string]error),
		IPMap:  make(map[string]string),
	}
}

// Name return the name of fake CNI plugin.
func (f *FakeCNIPlugin) Name() string {
	return "fake-CNI-plugin"
}

// SetUpPod setup the network of PodSandbox.
func (f *FakeCNIPlugin) SetUpPod(podNetwork ocicni.PodNetwork) error {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("SetUpPod", podNetwork)
	if err := f.getError("SetUpPod"); err != nil {
		return err
	}
	f.IPMap[podNetwork.NetNS] = generateIP()
	return nil
}

// TearDownPod teardown the network of PodSandbox.
func (f *FakeCNIPlugin) TearDownPod(podNetwork ocicni.PodNetwork) error {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("TearDownPod", podNetwork)
	if err := f.getError("TearDownPod"); err != nil {
		return err
	}
	_, ok := f.IPMap[podNetwork.NetNS]
	if !ok {
		return fmt.Errorf("failed to find the IP")
	}
	delete(f.IPMap, podNetwork.NetNS)
	return nil
}

// GetPodNetworkStatus get the status of network.
func (f *FakeCNIPlugin) GetPodNetworkStatus(podNetwork ocicni.PodNetwork) (string, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("GetPodNetworkStatus", podNetwork)
	if err := f.getError("GetPodNetworkStatus"); err != nil {
		return "", err
	}
	ip, ok := f.IPMap[podNetwork.NetNS]
	if !ok {
		return "", fmt.Errorf("failed to find the IP")
	}
	return ip, nil
}

// Status get the status of the plugin.
func (f *FakeCNIPlugin) Status() error {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("Status", nil)
	return f.getError("Status")
}

func generateIP() string {
	rand.Seed(time.Now().Unix())
	p1 := strconv.Itoa(rand.Intn(266))
	p2 := strconv.Itoa(rand.Intn(266))
	p3 := strconv.Itoa(rand.Intn(266))
	p4 := strconv.Itoa(rand.Intn(266))
	return p1 + "." + p2 + "." + p3 + "." + p4
}
