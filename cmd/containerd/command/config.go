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
	"io"
	"os"
	"sort"
	"text/template"

	"github.com/BurntSushi/toml"
	"github.com/containerd/containerd/server"
	"github.com/urfave/cli"
)

var commentedContainerdConfigTemplate = template.Must(template.New("containerd").Parse(
	`# Root is the path to a directory where containerd will store persistent data
root = "{{ .Root }}"

# State is the path to a directory where containerd will store transient data
state = "{{ .State }}"

# OOMScore adjust the containerd's oom score
oom_score = {{ .OOMScore }}

# GRPC configuration settings
[grpc]
  address = "{{ .GRPC.Address }}"
  uid = {{ .GRPC.UID }}
  gid = {{ .GRPC.GID }}

# Debug and profiling settings
[debug]
  address = "{{ .Debug.Address }}"
  uid = {{ .Debug.UID }}
  gid = {{ .Debug.GID }}
  level = "{{ .Debug.Level }}"

# Metrics and monitoring settings
[metrics]
  address = "{{ .Metrics.Address }}"
  grpc_histogram = {{ .Metrics.GRPCHistogram }}

# Cgroup specifies cgroup information for the containerd daemon process
[cgroup]
  path = "{{ .Cgroup.Path }}"

# Plugins overrides "Plugins map[string]toml.Primitive" in server config.
[Plugins]
`))

// Config is a wrapper of server config for printing out.
type Config struct {
	*server.Config
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
			Flags: []cli.Flag{
				cli.BoolFlag{
					Name:  "comment",
					Usage: "Add comments or not for config items",
				},
			},
			Action: func(context *cli.Context) error {
				config := &Config{
					Config: defaultConfig(),
				}

				if context.Bool("comment") {
					err := commentedContainerdConfigTemplate.ExecuteTemplate(os.Stdout, "containerd", config)
					if err != nil {
						return err
					}
				}

				plugins, err := server.LoadPlugins(config.Config)
				if err != nil {
					return err
				}
				sort.SliceStable(plugins, func(i, j int) bool {
					return plugins[i].ID < plugins[j].ID
				})
				if len(plugins) != 0 {
					config.Plugins = make(map[string]interface{})
					for _, p := range plugins {
						if p.Config == nil {
							continue
						}
						if context.Bool("comment") && p.CommentedConfigTemplate != nil {
							err := p.CommentedConfigTemplate.ExecuteTemplate(os.Stdout, p.ID, p.Config)
							if err != nil {
								return err
							}
						}
						config.Plugins[p.ID] = p.Config
					}
				}

				if !context.Bool("comment") {
					_, err = config.WriteTo(os.Stdout)
				}
				return err
			},
		},
	},
}
