//go:build linux
// +build linux

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

	"github.com/intel/goresctrl/pkg/blockio"
)

var blockIOEnabled bool

func BlockIOEnabled() bool { return blockIOEnabled }

func initBlockIO(configFilePath string) error {
	blockIOEnabled = false

	if configFilePath == "" {
		log.L.Debug("No blockio config file specified, blockio not configured")
		return nil
	}

	if err := blockio.SetConfigFromFile(configFilePath, true); err != nil {
		return fmt.Errorf("blockio not enabled: %w", err)
	}

	blockIOEnabled = true

	return nil

}
