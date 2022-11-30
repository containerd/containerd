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
	"github.com/containerd/containerd/pkg/nri"
	"github.com/containerd/containerd/plugin"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type:   plugin.NRIApiPlugin,
		ID:     "nri",
		Config: nri.DefaultConfig(),
		InitFn: initFunc,
	})
}

func initFunc(ic *plugin.InitContext) (interface{}, error) {
	l, err := nri.New(ic.Config.(*nri.Config))
	return l, err
}
