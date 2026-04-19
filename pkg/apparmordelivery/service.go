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

package apparmordelivery

import (
	"context"
	"errors"

	"github.com/containerd/containerd/v2/pkg/securityprofile"
)

var (
	ErrProfileNotFound    = errors.New("profile not found in image labels")
	ErrInvalidProfileData = errors.New("invalid profile data")
	ErrUnsupported        = errors.New("apparmor profile delivery unsupported on this platform")
)

type Service interface {
	EnsureProfile(ctx context.Context, profileRef string, labels map[string]string) (string, error)
}

type Config = securityprofile.Config

var defaultConfig = securityprofile.Config{
	LabelPrefix: "io.containerd.apparmor.localhost/",
	TargetDir:   "/etc/apparmor.d/profiles",
}
