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

package main

import (
	"fmt"
	"io/ioutil"
	"log"

	"github.com/pelletier/go-toml"
)

type config struct {
	Version    string
	Generators []string

	// Generator is a code generator which is used from protoc.
	// Deprecated: Use Generators instead.
	Generator string

	// Parameters are custom parameters to be passed to the generators.
	// The parameter key must be the generator name with a table value
	// of keys and string values to be passed.
	// Example:
	// [parameters.go-ttrpc]
	// customkey = "somevalue"
	Parameters map[string]map[string]string

	// Plugins will be deprecated. It has to be per-Generator setting,
	// but neither protoc-gen-go nor protoc-gen-go-grpc support plugins.
	// So the refactoring is not worth to do.
	Plugins []string

	Includes struct {
		Before   []string
		Vendored []string
		Packages []string
		After    []string
	}

	Packages map[string]string

	Overrides []struct {
		Prefixes []string
		// Generator is a code generator which is used from protoc.
		// Deprecated: Use Generators instead.
		Generator  string
		Generators []string
		Parameters map[string]map[string]string
		Plugins    *[]string

		// TODO(stevvooe): We could probably support overriding of includes and
		// package maps, but they don't seem to be as useful. Likely,
		// overriding the package map is more useful but includes happen
		// project-wide.
	}

	Descriptors []struct {
		Prefix      string
		Target      string
		IgnoreFiles []string `toml:"ignore_files"`
	}
}

func newDefaultConfig() config {
	return config{
		Includes: struct {
			Before   []string
			Vendored []string
			Packages []string
			After    []string
		}{
			Before: []string{"."},
			After:  []string{"/usr/local/include", "/usr/include"},
		},
	}
}

func readConfig(path string) (config, error) {
	p, err := ioutil.ReadFile(path)
	if err != nil {
		log.Fatalln(err)
	}
	return readConfigFrom(p)
}

func readConfigFrom(p []byte) (config, error) {
	c := newDefaultConfig()
	if err := toml.Unmarshal(p, &c); err != nil {
		log.Fatalln(err)
	}

	if c.Generator != "" {
		if len(c.Generators) > 0 {
			return config{}, fmt.Errorf(
				`specify either "generators = %v" or "generator = %v", not both`,
				c.Generators, c.Generator,
			)
		}
		c.Generators = []string{c.Generator}
		c.Generator = ""
	}

	for i, o := range c.Overrides {
		if o.Generator != "" {
			if len(o.Generators) > 0 {
				return config{}, fmt.Errorf(
					`specify either "overrides[%d].generators" or "overrides[%d].generator", not both`,
					i, i,
				)
			}
			c.Overrides[i].Generators = []string{o.Generator}
			c.Overrides[i].Generator = ""
		}
	}

	if len(c.Generators) == 0 {
		c.Generators = []string{"go"}
	}

	return c, nil
}
