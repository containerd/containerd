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
	"fmt"
	"net/url"
	"strings"

	"github.com/containerd/containerd/reference"
)

// SourceLabel determines the source label key and value using the given
// reference and existing set of source labels
func SourceLabel(ctx context.Context, ref string, labels map[string]string) (string, string, error) {
	refspec, err := reference.Parse(ref)
	if err != nil {
		return "", "", err
	}

	u, err := url.Parse("dummy://" + refspec.Locator)
	if err != nil {
		return "", "", err
	}
	key := "containerd.io/distribution.source." + u.Host
	value := strings.TrimPrefix(u.Path, "/")

	if label := labels[key]; label != "" {
		// TODO: Check if it is already there and remove if so
		value = fmt.Sprintf("%s,%s", value, label)

	}
	return key, value, nil
}

func chooseSource(ctx context.Context, refspec reference.Spec, annotations map[string]string) string {
	u, err := url.Parse("dummy://" + refspec.Locator)
	if err != nil {
		return ""
	}
	key := "containerd.io/distribution.source." + u.Host

	value := annotations[key]
	if value == "" {
		return ""
	}

	sources := strings.Split(value, ",")
	path := strings.TrimPrefix(u.Path, "/")
	best := -1
	match := ""

	for _, source := range sources {
		if source == path {
			continue
		}
		if l := prefixLen(source, path); l > best {
			match = source
			best = l
		}
	}

	return match
}

func prefixLen(s, prefix string) int {
	var i int
	for ; i < len(prefix) && i < len(s); i++ {
		if s[i] != prefix[i] {
			break
		}
	}
	return i
}
