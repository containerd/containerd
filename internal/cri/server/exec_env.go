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

import "strings"

// mergeExecEnv merges overlay environment variables into the base process
// environment. If a key exists in both base and overlay, the overlay value
// wins. New keys from overlay are appended in their original order.
//
// Both base and overlay use "KEY=VALUE" format strings. The key is the
// portion before the first '='. Values may contain '=' characters.
func mergeExecEnv(base, overlay []string) []string {
	if len(overlay) == 0 {
		return base
	}

	// Parse overlay into a map (last entry wins for duplicates)
	overrideMap := make(map[string]string, len(overlay))
	order := make([]string, 0, len(overlay))
	seen := make(map[string]struct{}, len(overlay))

	for _, entry := range overlay {
		k, v := splitEnvEntry(entry)
		if k == "" {
			continue
		}
		overrideMap[k] = v
		if _, exists := seen[k]; !exists {
			seen[k] = struct{}{}
			order = append(order, k)
		}
	}

	// Track which keys exist in base
	baseKeys := make(map[string]struct{}, len(base))
	for _, entry := range base {
		k, _ := splitEnvEntry(entry)
		if k != "" {
			baseKeys[k] = struct{}{}
		}
	}

	// Build result: base entries with overrides applied in-place
	result := make([]string, 0, len(base)+len(order))
	for _, entry := range base {
		k, _ := splitEnvEntry(entry)
		if k == "" {
			result = append(result, entry)
			continue
		}
		if val, override := overrideMap[k]; override {
			result = append(result, k+"="+val)
		} else {
			result = append(result, entry)
		}
	}

	// Append new keys not present in base
	for _, k := range order {
		if _, inBase := baseKeys[k]; !inBase {
			result = append(result, k+"="+overrideMap[k])
		}
	}

	return result
}

// splitEnvEntry splits "KEY=VALUE" into key and value.
// If there is no '=', key is the entire string and value is empty.
func splitEnvEntry(entry string) (key, value string) {
	parts := strings.SplitN(entry, "=", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "", ""
	}
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], ""
}
