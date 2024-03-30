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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	transfertypes "github.com/containerd/containerd/v2/api/types/transfer"
	"github.com/containerd/containerd/v2/defaults"
)

var (
	DefaultPath = filepath.Join(defaults.DefaultConfigDir, "auths.json")
)

type fileStore struct {
	path string
	sync.RWMutex
}

type AuthInfos struct {
	Auths map[string]map[string]string `json:"auths"`
}

func newFileStore(filePaths ...string) Manager {
	filePath := DefaultPath
	if len(filePaths) > 0 {
		filePath = filePaths[0]
	}
	_, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		dir, _ := filepath.Split(filePath)
		err := os.MkdirAll(dir, os.ModeDir)
		if err != nil {
			panic(err)
		}
		file, err := os.Create(filePath)
		if err != nil {
			panic(err)
		}
		file.Close()
	}
	return &fileStore{path: filePath}
}

func (f *fileStore) Store(ctx context.Context, cred Credentials) error {
	f.Lock()
	defer f.Unlock()
	bytes, err := os.ReadFile(f.path)
	if err != nil {
		return err
	}

	var authInfo AuthInfos
	if len(bytes) > 0 {
		err = json.Unmarshal(bytes, &authInfo)
		if err != nil {
			return err
		}
	}

	host := cred.Host
	parse, err := url.Parse(cred.Host)
	if err != nil {
		return err
	}
	if parse.Host != "" {
		host = parse.Host
	}

	if authInfo.Auths == nil {
		authInfo.Auths = make(map[string]map[string]string)
	}

	authInfo.Auths[host] = map[string]string{
		"auth": EncodeUserNameAndSecret(cred.Username, cred.Secret),
	}

	bytes, err = json.Marshal(authInfo)
	if err != nil {
		return err
	}
	err = os.WriteFile(f.path, bytes, 0644)
	if err != nil {
		return err
	}
	return nil
}

func (f *fileStore) Get(ctx context.Context, request *transfertypes.AuthRequest) (*transfertypes.AuthResponse, error) {
	f.RLock()
	defer f.RUnlock()
	bytes, err := os.ReadFile(f.path)
	if err != nil {
		return &transfertypes.AuthResponse{}, err
	}

	var authInfo AuthInfos
	if len(bytes) > 0 {
		err = json.Unmarshal(bytes, &authInfo)
		if err != nil {
			return &transfertypes.AuthResponse{}, err
		}
	}

	parse, err := url.Parse(request.Host)
	if err != nil {
		return &transfertypes.AuthResponse{}, err
	}
	if parse.Host != "" {
		request.Host = parse.Host
	}

	if authInfo.Auths == nil {
		authInfo.Auths = make(map[string]map[string]string)
	}

	if auth, ok := authInfo.Auths[request.Host]; ok {
		userName, secret, err := DecodeUserNameAndSecret(auth["auth"])
		if err != nil {
			return &transfertypes.AuthResponse{}, err
		}
		return &transfertypes.AuthResponse{
			AuthType: transfertypes.AuthType_CREDENTIALS,
			Username: userName,
			Secret:   secret,
		}, nil
	}
	return &transfertypes.AuthResponse{}, errors.New("not found")
}

func (f *fileStore) Delete(ctx context.Context, cred Credentials) error {
	f.Lock()
	defer f.Unlock()
	bytes, err := os.ReadFile(f.path)
	if err != nil {
		return err
	}

	var authInfo AuthInfos
	if len(bytes) > 0 {
		err = json.Unmarshal(bytes, &authInfo)
		if err != nil {
			return err
		}
	}

	host := cred.Host
	parse, err := url.Parse(cred.Host)
	if err != nil {
		return err
	}
	if parse.Host != "" {
		host = parse.Host
	}

	if authInfo.Auths == nil {
		return nil
	}

	delete(authInfo.Auths, host)
	bytes, err = json.Marshal(authInfo)
	if err != nil {
		return err
	}
	err = os.WriteFile(f.path, bytes, 0644)
	if err != nil {
		return err
	}
	return nil
}

func EncodeUserNameAndSecret(username, secret string) string {
	data := fmt.Sprintf("%s:%s", username, secret)
	return base64.StdEncoding.EncodeToString([]byte(data))
}

func DecodeUserNameAndSecret(data string) (string, string, error) {
	bytes, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", "", err
	}
	datas := strings.Split(string(bytes), ":")
	return datas[0], datas[1], err
}
