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

package io

import "github.com/docker/go-metrics"

var (
	inputEntries  metrics.Counter
	outputEntries metrics.Counter
	inputBytes    metrics.Counter
	outputBytes   metrics.Counter
	splitEntries  metrics.Counter
)

func init() {
	// These CRI metrics record input and output logging volume.
	ns := metrics.NewNamespace("containerd", "cri", nil)

	inputEntries = ns.NewCounter("input_entries", "Number of log entries received")
	outputEntries = ns.NewCounter("output_entries", "Number of log entries successfully written to disk")
	inputBytes = ns.NewCounter("input_bytes", "Size of logs received")
	outputBytes = ns.NewCounter("output_bytes", "Size of logs successfully written to disk")
	splitEntries = ns.NewCounter("split_entries", "Number of extra log entries created by splitting the "+
		"original log entry. This happens when the original log entry exceeds length limit. "+
		"This metric does not count the original log entry.")

	metrics.Register(ns)
}
