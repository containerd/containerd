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

package compat

import (
	"fmt"

	gocni "github.com/containerd/go-cni"
	types100 "github.com/containernetworking/cni/pkg/types/100"
)

type Result struct {
	gocni.Result
	Labels map[string]string
	raw    []*types100.Result
}

// Raw returns the raw CNI results of multiple networks.
func (r *Result) Raw() []*types100.Result {
	return r.raw
}

// createResult creates a Result from the given slice of types100.Result, adding
// structured data containing the interface configuration for each of the
// interfaces created in the namespace. It returns an error if validation of
// results fails, or if a network could not be found.
func (c *impl) createResult(results []*attachResult) (*Result, error) {
	c.RLock()
	defer c.RUnlock()
	r := &Result{
		Result: gocni.Result{
			Interfaces: make(map[string]*gocni.Config),
		},
		Labels: make(map[string]string),
		//raw:    results,
	}

	// Plugins may not need to return Interfaces in result if
	// there are no multiple interfaces created. In that case
	// all configs should be applied against default interface
	r.Interfaces[defaultInterface(c.Prefix)] = &gocni.Config{}

	// Walk through all the results
	for _, result := range results {
		// Walk through all the interface in each result
		for _, intf := range result.r.Interfaces {
			r.Interfaces[intf.Name] = &gocni.Config{
				Mac:     intf.Mac,
				Sandbox: intf.Sandbox,
			}
		}
		// Walk through all the IPs in the result and attach it to corresponding
		// interfaces
		for _, ipConf := range result.r.IPs {
			if err := validateInterfaceConfig(ipConf, len(result.r.Interfaces)); err != nil {
				return nil, fmt.Errorf("invalid interface config: %v: %w", err, gocni.ErrInvalidResult)
			}
			name := c.getInterfaceName(result.r.Interfaces, ipConf)
			r.Interfaces[name].IPConfigs = append(r.Interfaces[name].IPConfigs,
				&gocni.IPConfig{IP: ipConf.Address.IP, Gateway: ipConf.Gateway})
		}
		r.DNS = append(r.DNS, result.r.DNS)
		r.Routes = append(r.Routes, result.r.Routes...)

		// Walk through all owner labels in the result
		for k, v := range result.l {
			r.Labels[k] = v
		}
	}
	if _, ok := r.Interfaces[defaultInterface(c.Prefix)]; !ok {
		return nil, fmt.Errorf("default network not found for: %s: %w", defaultInterface(c.Prefix), gocni.ErrNotFound)
	}
	return r, nil
}

// getInterfaceName returns the interface name if the plugins
// return the result with associated interfaces. If interface
// is not present then default interface name is used
func (c *impl) getInterfaceName(interfaces []*types100.Interface,
	ipConf *types100.IPConfig) string {
	if ipConf.Interface != nil {
		return interfaces[*ipConf.Interface].Name
	}
	return defaultInterface(c.Prefix)
}

func WrapResult(result *gocni.Result) *Result {
	return &Result{
		Result: *result,
		raw:    result.Raw(),
	}
}
