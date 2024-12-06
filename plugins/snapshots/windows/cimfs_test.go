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

package windows

import (
	"encoding/json"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
)

func TestGetOptionByPrefix(t *testing.T) {
	cimPath := "C:\\fake\\cim\\path.cim"
	cimPathOpt := LayerCimPathFlag + cimPath
	parentCimPaths := []string{"C:\\fake\\parent1.cim", "C:\\fake\\parent2.cim"}
	parentCimLayersJSON, _ := json.Marshal(parentCimPaths)
	parentCimLayersOpt := ParentLayerCimPathsFlag + string(parentCimLayersJSON)
	m := &mount.Mount{
		Source:  "C:\\foo\\bar",
		Type:    "CimFS",
		Options: []string{cimPathOpt, parentCimLayersOpt},
	}

	parsedCimPath, err := GetCimPath(m)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if cimPath != parsedCimPath {
		t.Errorf("Expected `%s`, got `%s`", cimPath, parsedCimPath)
	}

	parsedParentCimPaths, err := GetParentCimPaths(m)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if len(parsedParentCimPaths) != 2 ||
		parsedParentCimPaths[0] != parentCimPaths[0] ||
		parsedParentCimPaths[1] != parentCimPaths[1] {
		t.Errorf("Expected `%v`, got `%v`", parentCimPaths, parsedParentCimPaths)
	}
}
