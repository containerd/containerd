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

package config

// TracingConfig holds tracing settings used by CRI to initialize TraceManager.
type TracingConfig struct {
	Enabled          bool             `toml:"enabled"`
	SamplingRate     float64          `toml:"sampling_rate"`
	UseSandboxID     bool             `toml:"use_sandbox_id"`
	MaxSpansPerTrace int              `toml:"max_spans_per_trace"`
	Exporters        []ExporterConfig `toml:"exporters"`
}

// ExporterConfig describes one tracing exporter endpoint and options.
type ExporterConfig struct {
	Type     string                 `toml:"type" json:"type"`
	Endpoint string                 `toml:"endpoint" json:"endpoint"`
	Options  map[string]interface{} `toml:"options" json:"options"`
}
