//go:build !windows && !linux

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

package server

import (
	"fmt"

	"github.com/containerd/containerd/v2/pkg/netns"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func (c *criService) bringUpLoopback(string) error {
	return nil
}

func (c *criService) setupNetnsWithinUserns(basedir string, cfg *runtime.UserNamespace) (*netns.NetNS, error) {
	return nil, fmt.Errorf("unsupported to setup netns within userns on unix platform")
}
