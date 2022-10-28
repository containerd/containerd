//go:build linux && !no_rdt
// +build linux,!no_rdt

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

package tasks

import (
	"fmt"

	"github.com/containerd/containerd/log"

	"github.com/intel/goresctrl/pkg/rdt"
)

const (
	// ResctrlPrefix is the prefix used for class/closid directories under the resctrl filesystem
	ResctrlPrefix = ""
)

var rdtEnabled bool

func RdtEnabled() bool { return rdtEnabled }

func initRdt(configFilePath string) error {
	rdtEnabled = false

	if configFilePath == "" {
		log.L.Debug("No RDT config file specified, RDT not configured")
		return nil
	}

	if err := rdt.Initialize(ResctrlPrefix); err != nil {
		return fmt.Errorf("RDT not enabled: %w", err)
	}

	if err := rdt.SetConfigFromFile(configFilePath, true); err != nil {
		return err
	}

	rdtEnabled = true

	return nil

}
