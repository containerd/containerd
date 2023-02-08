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

package sbserver

import (
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/containerd/containerd/pkg/rdt"
)

// rdtClassFromAnnotations examines container and pod annotations of a
// container and returns its effective RDT class.
func (c *criService) rdtClassFromAnnotations(containerName string, containerAnnotations, podAnnotations map[string]string) (string, error) {
	cls, err := rdt.ContainerClassFromAnnotations(containerName, containerAnnotations, podAnnotations)

	if err == nil {
		// Our internal check that RDT has been enabled
		if cls != "" && !rdt.IsEnabled() {
			err = fmt.Errorf("RDT disabled, refusing to set RDT class of container %q to %q", containerName, cls)
		}
	}

	if err != nil {
		if !rdt.IsEnabled() && c.config.ContainerdConfig.IgnoreRdtNotEnabledErrors {
			logrus.Debugf("continuing create container %s, ignoring rdt not enabled (%v)", containerName, err)
			return "", nil
		}
		return "", err
	}

	return cls, nil
}
