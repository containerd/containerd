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

package docker

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/plugins/credential"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/containerd/containerd/remotes/docker/config"
)

type Config struct {
	ConfigPath string `toml:"config_path" json:"configPath"`
}

func init() {
	plugin.Register(&plugin.Registration{
		Type:   plugin.RegistryPlugin,
		ID:     "registry",
		Config: &Config{},
		Requires: []plugin.Type{
			plugin.CredentialPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			plugins, err := ic.GetByType(plugin.CredentialPlugin)
			if err != nil {
				return nil, err
			}

			m := make(map[string]credential.Credential, len(plugins))
			for _, p := range plugins {
				i, err := p.Instance()
				if err != nil {
					return nil, err
				}
				m[p.Registration.ID] = i.(credential.Credential)
			}
			config, ok := ic.Config.(*Config)
			if !ok {
				return nil, errors.New("invalid registry service configuration")
			}
			if len(config.ConfigPath) == 0 {
				// TODO
				config.ConfigPath = "/etc/containerd/certs.d/"
			}

			return &Manager{plugins: m, configPath: config.ConfigPath}, nil
		},
	})
}

type Manager struct {
	plugins    map[string]credential.Credential
	configPath string
}

func (m *Manager) RegistryHosts(ctx context.Context, opt config.HostOptions) docker.RegistryHosts {
	paths := filepath.SplitList(m.configPath)
	hostDirFn := hostDirFromRoots(paths)
	opt.HostDir = hostDirFn
	if opt.Credentials == nil {
		opt.Credentials = func(host string) (string, string, error) {
			dir, err := hostDirFn(host)
			if err != nil && !errdefs.IsNotFound(err) {
				return "", "", err
			}
			hosts, err := config.LoadHostDir(ctx, dir)
			if err != nil {
				return "", "", err
			}
			for _, h := range hosts {
				if h.host != host {
					continue
				}
				// read only, not need lock
				cred, ok := m.plugins[h.credentialPlugin]
				if !ok {
					return "", "", fmt.Errorf("not found credential plugin %q", h.credentialPlugin)
				}
				a, err := cred.Get(context.Background(), host)
				if err != nil {
					return "", "", err
				}
				return a.Username, a.Secret, nil
			}
			return "", "", nil
		}
	}
	return config.ConfigureHosts(ctx, opt)
}

func hostDirFromRoots(roots []string) func(string) (string, error) {
	rootfn := make([]func(string) (string, error), len(roots))
	for i := range roots {
		rootfn[i] = config.HostDirFromRoot(roots[i])
	}
	return func(host string) (dir string, err error) {
		for _, fn := range rootfn {
			dir, err = fn(host)
			if (err != nil && !errdefs.IsNotFound(err)) || (dir != "") {
				break
			}
		}
		return
	}
}
