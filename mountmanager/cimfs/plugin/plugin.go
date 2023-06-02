//go:build windows
// +build windows

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

package plugin

import (
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mountmanager/cimfs"
	"github.com/containerd/containerd/plugin"
)

// Plugin is defined in a separate package so that the types defined by this plugin (under mountmanager/cimfs)
// can be imported by other binaries (such as the shim) without automatically calling this init method.
func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.MountPlugin,
		ID:   "mount-cimfs",
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			log.G(ic.Context).Info("initializing cimfs mount manager plugin")
			return cimfs.NewCimfsMountManager()
		},
	})
}
