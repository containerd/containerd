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

import (
	"runtime"
	"testing"
)

func TestHostPathsUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Test only valid on unix")
	}

	// Test most common case
	expected := []string{"docker.io", ".docker.io", "_default"}
	paths := hostPaths("", "docker.io")
	compareLists(t, paths, expected)

	// Test w/ tenant
	expected = []string{"test.docker.io", ".docker.io", "_default"}
	paths = hostPaths("", "test.docker.io")
	compareLists(t, paths, expected)

	// Test w/ multi segments
	// Since .io is technically a country-code, it falls under the second-level hierarchy TLD logic
	expected = []string{"foo.bar.test.docker.io", ".test.docker.io", "_default"}
	paths = hostPaths("", "foo.bar.test.docker.io")
	compareLists(t, paths, expected)

	// Test w/ multi segments and non ccTLD
	expected = []string{"foo.bar.test.docker.com", ".docker.com", "_default"}
	paths = hostPaths("", "foo.bar.test.docker.com")
	compareLists(t, paths, expected)

	// Test w/ localhost
	expected = []string{"localhost", ".localhost", "_default"}
	paths = hostPaths("", "localhost")
	compareLists(t, paths, expected)

	// Test w/ localhost and port
	expected = []string{"localhost_5555_", "localhost:5555", ".localhost_5555_", "_default"}
	paths = hostPaths("", "localhost:5555")
	compareLists(t, paths, expected)

	// Test w/ localhost domain
	expected = []string{"test.localhost", ".test.localhost", "_default"}
	paths = hostPaths("", "test.localhost")
	compareLists(t, paths, expected)

	// Test w/ localhost domain and tenant
	expected = []string{"specific.test.localhost", ".test.localhost", "_default"}
	paths = hostPaths("", "specific.test.localhost")
	compareLists(t, paths, expected)

	// Test w/ localhost domain and tenant and port
	expected = []string{"specific.test.localhost_5555_", "specific.test.localhost:5555", ".test.localhost_5555_", "_default"}
	paths = hostPaths("", "specific.test.localhost:5555")
	compareLists(t, paths, expected)

	// Test w/ non second-level hierarchy ccTLD and no subdomain
	expected = []string{"docker.us", ".docker.us", "_default"}
	paths = hostPaths("", "docker.us")
	compareLists(t, paths, expected)

	// Test w/ non second-level hierarchy ccTLD and subdomain
	expected = []string{"test.docker.us", ".docker.us", "_default"}
	paths = hostPaths("", "test.docker.us")
	compareLists(t, paths, expected)

	// Test w/ second-level hierarchy ccTLD and no subdomain
	expected = []string{"docker.co.uk", ".docker.co.uk", "_default"}
	paths = hostPaths("", "docker.co.uk")
	compareLists(t, paths, expected)

	// Test w/ second-level hierarchy ccTLD and subdomain
	expected = []string{"test.docker.co.uk", ".docker.co.uk", "_default"}
	paths = hostPaths("", "test.docker.co.uk")
	compareLists(t, paths, expected)

	// Test w/ second-level hierarchy ccTLD w/ multiple-segment subdomain
	expected = []string{"more.test.docker.co.uk", ".docker.co.uk", "_default"}
	paths = hostPaths("", "more.test.docker.co.uk")
	compareLists(t, paths, expected)

	// Test w/ second-level hierarchy ccTLD and 3-letter second-level domain
	expected = []string{"more.test.docker.org.uk", ".docker.org.uk", "_default"}
	paths = hostPaths("", "more.test.docker.org.uk")
	compareLists(t, paths, expected)

	// Test w/ second-level hierarchy ccTLD w/ port
	expected = []string{"test.docker.co.uk_5555_", "test.docker.co.uk:5555", ".docker.co.uk_5555_", "_default"}
	paths = hostPaths("", "test.docker.co.uk:5555")
	compareLists(t, paths, expected)
}
