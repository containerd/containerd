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

	transfertypes "github.com/containerd/containerd/v2/api/types/transfer"
	"github.com/docker/docker-credential-helpers/client"
	"github.com/docker/docker-credential-helpers/credentials"
)

const (
	DefaultBinaryNamePrefix = "docker-credential-"
)

type dockerCredential struct {
	credentialBinaryName string
}

func newDockerCredential(credsStore string) Manager {
	return &dockerCredential{
		credentialBinaryName: DefaultBinaryNamePrefix + credsStore,
	}
}

func (d *dockerCredential) Store(ctx context.Context, cred Credentials) error {
	shellProgramFunc := client.NewShellProgramFunc(d.credentialBinaryName)
	return client.Store(shellProgramFunc, &credentials.Credentials{
		ServerURL: cred.Host,
		Username:  cred.Username,
		Secret:    cred.Secret,
	})
}

func (d *dockerCredential) Get(ctx context.Context, request *transfertypes.AuthRequest) (*transfertypes.AuthResponse, error) {
	shellProgramFunc := client.NewShellProgramFunc(d.credentialBinaryName)
	creds, err := client.Get(shellProgramFunc, request.Host)
	if err != nil {
		return &transfertypes.AuthResponse{}, err
	}
	return &transfertypes.AuthResponse{
		AuthType: transfertypes.AuthType_CREDENTIALS,
		Username: creds.Username,
		Secret:   creds.Secret,
	}, nil
}

func (d *dockerCredential) Delete(ctx context.Context, cred Credentials) error {
	shellProgramFunc := client.NewShellProgramFunc(d.credentialBinaryName)
	return client.Erase(shellProgramFunc, cred.Host)
}
