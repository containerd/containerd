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
	"bytes"
	"encoding/json"
	"os/exec"

	"github.com/containerd/containerd/pkg/cri/config"
	"github.com/pkg/errors"
	v1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type ImagePullCreds struct {
	Host   string `json:"host"`
	User   string `json:"user"`
	Secret string `json:"secret"`
}

func ImageDistributionHelperEnabled(c *config.Config) bool {
	return c.ImageDistributionHelper.BinaryPath != ""
}

func GetImageDistributionHelperBinary(c *config.Config) string {
	return c.ImageDistributionHelper.BinaryPath
}

func prepareCreds(host string, auth *v1.AuthConfig) ([]byte, error) {
	user, secret, err := ParseAuth(auth, host)
	if err != nil {
		return nil, errors.Wrap(err, "parse auth")
	}

	pullCreds := ImagePullCreds{
		Host:   host,
		User:   user,
		Secret: secret,
	}

	jo, err := json.Marshal(&pullCreds)
	if err != nil {
		return nil, errors.Wrap(err, "marshal credential")
	}

	return jo, nil
}

func InvokeImageDistributionHelper(c *config.Config, host string, auth *v1.AuthConfig) error {
	creds, err := prepareCreds(host, auth)
	if err != nil {
		return err
	}

	cmd := exec.Command(GetImageDistributionHelperBinary(c))
	cmd.Stdin = bytes.NewBuffer(creds)

	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, "invoke image distribution hook")
	}

	return nil
}
