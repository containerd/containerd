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

package command

import (
	gocontext "context"
	"io"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/containerd/containerd/pkg/timeout"
	"github.com/containerd/containerd/services/server"
	srvconfig "github.com/containerd/containerd/services/server/config"
	"github.com/urfave/cli"
)

// Config is a wrapper of server config for printing out.
type Config struct {
	*srvconfig.Config
	// Plugins overrides `Plugins map[string]toml.Primitive` in server config.
	Plugins map[string]interface{} `toml:"plugins"`
}

// WriteTo marshals the config to the provided writer
func (c *Config) WriteTo(w io.Writer) (int64, error) {
	return 0, toml.NewEncoder(w).Encode(c)
}

var configCommand = cli.Command{
	Name:  "config",
	Usage: "information on the containerd config",
	Subcommands: []cli.Command{
		{
			Name:  "default",
			Usage: "see the output of the default config",
			Action: func(context *cli.Context) error {
				config := &Config{
					Config: defaultConfig(),
				}
				// for the time being, keep the defaultConfig's version set at 1 so that
				// when a config without a version is loaded from disk and has no version
				// set, we assume it's a v1 config.  But when generating new configs via
				// this command, generate the v2 config
				config.Config.Version = 2
				plugins, err := server.LoadPlugins(gocontext.Background(), config.Config)
				if err != nil {
					return err
				}
				if len(plugins) != 0 {
					config.Plugins = make(map[string]interface{})
					for _, p := range plugins {
						if p.Config == nil {
							continue
						}
						config.Plugins[p.URI()] = p.Config
					}
				}
				timeouts := timeout.All()
				config.Timeouts = make(map[string]string)
				for k, v := range timeouts {
					config.Timeouts[k] = v.String()
				}

				_, err = config.WriteTo(os.Stdout)
				return err
			},
		},
	},
}
