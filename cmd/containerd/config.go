package main

import (
	"io"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/containerd/containerd/server"
	"github.com/urfave/cli"
)

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
			Action: func(context *cli.Context) error {
				config := &Config{
					Config: defaultConfig(),
				}
				plugins, err := server.LoadPlugins(config.Config)
				if err != nil {
					return err
				}
				if len(plugins) != 0 {
					config.Plugins = make(map[string]interface{})
					for _, p := range plugins {
						if p.Config == nil {
							continue
						}
						config.Plugins[p.ID] = p.Config
					}
				}
				_, err = config.WriteTo(os.Stdout)
				return err
			},
		},
	},
}
