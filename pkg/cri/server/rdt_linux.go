//go:build !no_rdt

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

	"github.com/containerd/containerd/services/tasks"
	"github.com/intel/goresctrl/pkg/rdt"
	"github.com/sirupsen/logrus"
)

// rdtClassFromAnnotations examines container and pod annotations of a
// container and returns its effective RDT class.
func (c *criService) rdtClassFromAnnotations(containerName string, containerAnnotations, podAnnotations map[string]string) (string, error) {
	cls, err := rdt.ContainerClassFromAnnotations(containerName, containerAnnotations, podAnnotations)
	if err != nil {
		return "", err
	}

	if cls != "" && !tasks.RdtEnabled() {
		if c.config.ContainerdConfig.IgnoreRdtNotEnabledErrors {
			cls = ""
			logrus.Debugf("continuing create container %s, ignoring rdt not enabled (%v)", containerName, err)
		} else {
			return "", fmt.Errorf("RDT disabled, refusing to set RDT class of container %q to %q", containerName, cls)
		}
	}
	return cls, nil
}
