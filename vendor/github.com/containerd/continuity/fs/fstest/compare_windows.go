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

package fstest

var metadataFiles = map[string]bool{
	"\\System Volume Information":                true,
	"\\WcSandboxState":                           true,
	"\\WcSandboxState\\Hives":                    true,
	"\\WcSandboxState\\Hives\\DefaultUser_Delta": true,
	"\\WcSandboxState\\Hives\\Sam_Delta":         true,
	"\\WcSandboxState\\Hives\\Security_Delta":    true,
	"\\WcSandboxState\\Hives\\Software_Delta":    true,
	"\\WcSandboxState\\Hives\\System_Delta":      true,
	"\\WcSandboxState\\initialized":              true,
	"\\Windows":                                  true,
	"\\Windows\\System32":                        true,
	"\\Windows\\System32\\config":                true,
	"\\Windows\\System32\\config\\DEFAULT":       true,
	"\\Windows\\System32\\config\\SAM":           true,
	"\\Windows\\System32\\config\\SECURITY":      true,
	"\\Windows\\System32\\config\\SOFTWARE":      true,
	"\\Windows\\System32\\config\\SYSTEM":        true,
}
