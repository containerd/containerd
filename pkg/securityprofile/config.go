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

package securityprofile

// Config captures user-configurable delivery options shared by security profile services.
type Config struct {
	LabelPrefix string `toml:"label_prefix" json:"labelPrefix"`
	TargetDir   string `toml:"target_dir" json:"targetDir"`
}

// Normalize returns a copy of cfg merged with defaults, substituting any empty fields with the provided defaults.
func Normalize(cfg *Config, defaults Config) Config {
	result := defaults
	if cfg == nil {
		return result
	}
	if cfg.LabelPrefix != "" {
		result.LabelPrefix = cfg.LabelPrefix
	}
	if cfg.TargetDir != "" {
		result.TargetDir = cfg.TargetDir
	}
	return result
}
