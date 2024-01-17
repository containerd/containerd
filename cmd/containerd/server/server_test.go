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

import (
	"context"
	"testing"

	srvconfig "github.com/containerd/containerd/v2/cmd/containerd/server/config"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/stretchr/testify/assert"
)

const testPath = "/tmp/path/for/testing"

func TestCreateTopLevelDirectoriesErrorsWithSamePathForRootAndState(t *testing.T) {
	path := testPath
	err := CreateTopLevelDirectories(&srvconfig.Config{
		Root:  path,
		State: path,
	})
	assert.EqualError(t, err, "root and state must be different paths")
}

func TestCreateTopLevelDirectoriesWithEmptyStatePath(t *testing.T) {
	statePath := ""
	rootPath := testPath
	err := CreateTopLevelDirectories(&srvconfig.Config{
		Root:  rootPath,
		State: statePath,
	})
	assert.EqualError(t, err, "state must be specified")
}

func TestCreateTopLevelDirectoriesWithEmptyRootPath(t *testing.T) {
	statePath := testPath
	rootPath := ""
	err := CreateTopLevelDirectories(&srvconfig.Config{
		Root:  rootPath,
		State: statePath,
	})
	assert.EqualError(t, err, "root must be specified")
}

func TestMigration(t *testing.T) {
	registry.Reset()
	defer registry.Reset()

	version := srvconfig.CurrentConfigVersion - 1

	type testConfig struct {
		Migrated    string `toml:"migrated"`
		NotMigrated string `toml:"notmigrated"`
	}

	registry.Register(&plugin.Registration{
		Type:   "io.containerd.test",
		ID:     "t1",
		Config: &testConfig{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			c, ok := ic.Config.(*testConfig)
			if !ok {
				t.Error("expected first plugin to have configuration")
			} else {
				if c.Migrated != "" {
					t.Error("expected first plugin to have empty value for migrated config")
				}
				if c.NotMigrated != "don't migrate me" {
					t.Errorf("expected first plugin does not have correct value for not migrated config: %q", c.NotMigrated)
				}
			}
			return nil, nil
		},
	})
	registry.Register(&plugin.Registration{
		Type: "io.containerd.new",
		Requires: []plugin.Type{
			"io.containerd.test", // Ensure this test runs second
		},
		ID:     "t2",
		Config: &testConfig{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			c, ok := ic.Config.(*testConfig)
			if !ok {
				t.Error("expected second plugin to have configuration")
			} else {
				if c.Migrated != "migrate me" {
					t.Errorf("expected second plugin does not have correct value for migrated config: %q", c.Migrated)
				}
				if c.NotMigrated != "" {
					t.Error("expected second plugin to have empty value for not migrated config")
				}
			}
			return nil, nil
		},
		ConfigMigration: func(ctx context.Context, v int, plugins map[string]interface{}) error {
			if v != version {
				t.Errorf("unxpected version: %d", v)
			}
			t1, ok := plugins["io.containerd.test.t1"]
			if !ok {
				t.Error("plugin not set as expected")
				return nil
			}
			conf, ok := t1.(map[string]interface{})
			if !ok {
				t.Errorf("unexpected config value: %v", t1)
				return nil
			}
			newconf := map[string]interface{}{
				"migrated": conf["migrated"],
			}
			delete(conf, "migrated")
			plugins["io.containerd.new.t2"] = newconf

			return nil
		},
	})

	config := &srvconfig.Config{}
	config.Version = version
	config.Plugins = map[string]interface{}{
		"io.containerd.test.t1": map[string]interface{}{
			"migrated":    "migrate me",
			"notmigrated": "don't migrate me",
		},
	}

	ctx := context.Background()
	_, err := New(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
}
