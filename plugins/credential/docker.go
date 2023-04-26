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

package credential

import (
	"context"
	"errors"

	"github.com/containerd/containerd/plugin"
)

type Config struct {
	CredsStore string `json:"credsStore"`
}

func init() {
	plugin.Register(&plugin.Registration{
		Type:     plugin.CredentialPlugin,
		ID:       "docker-compatible",
		Config:   &Config{},
		Requires: []plugin.Type{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			config, ok := ic.Config.(*Config)
			if !ok {
				return nil, errors.New("invalid docker credential plugin configuration")
			}
			return &dockerStore{CredsStore: config.CredsStore}, nil
		},
	})
}

type dockerStore struct {
	CredsStore string
}

func (d *dockerStore) Store(ctx context.Context, auth AuthInfo) error {
	return nil
}
func (d *dockerStore) Get(ctx context.Context, host string) (AuthInfo, error) {
	return AuthInfo{}, nil
}
func (d *dockerStore) Delete(ctx context.Context, auth AuthInfo) error {
	return nil
}
func (d *dockerStore) List(ctx context.Context) ([]AuthInfo, error) {
	return nil, nil
}
