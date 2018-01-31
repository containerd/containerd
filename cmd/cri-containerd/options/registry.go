/*
Copyright 2018 The Containerd Authors.

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

package options

import (
	"fmt"
	"net/url"
	"strings"
)

// Mirror contains the config related to the registry mirror
type Mirror struct {
	Endpoints []string `toml:"endpoint" json:"endpoint,omitempty"`
	// TODO (Abhi) We might need to add auth per namespace. Looks like
	// image auth information is passed by kube itself.
}

// Registry is registry settings configured
type Registry struct {
	Mirrors map[string]Mirror `toml:"mirrors" json:"mirrors,omitempty"`
}

// String returns the string format of registry type
func (r *Registry) String() string {
	// Its not used hence return empty string
	return ""
}

// Set validates and converts into the internal registry struct
func (r *Registry) Set(s string) error {
	// --registry docker.io=https://mymirror.io,http://mymirror2.io
	// If no option is set then return format error
	if len(s) == 0 {
		return fmt.Errorf("incomplete registry mirror option")
	}
	var mirrors []string
	host := "docker.io"
	opt := strings.Split(s, "=")
	if len(opt) > 1 {
		// If option is set in the format "mynamespace.io=https://mymirror.io,https://mymirror2.io"
		// Then associate the mirror urls for the namespace only"
		host = opt[0]
		mirrors = strings.Split(opt[1], ",")
	} else {
		// If option is set in the format "https://mymirror.io,https://mymirror.io"
		// Then associate mirror against default docker.io namespace
		mirrors = strings.Split(opt[0], ",")
	}

	// Validate the format of the urls passed
	for _, u := range mirrors {
		_, err := url.Parse(u)
		if err != nil {
			return fmt.Errorf("invalid registry mirror url format %v: %v", u, err)
		}
	}

	if r.Mirrors == nil {
		r.Mirrors = make(map[string]Mirror)
	}
	if _, ok := r.Mirrors[host]; !ok {
		r.Mirrors[host] = Mirror{}
	}
	m := r.Mirrors[host]
	m.Endpoints = append(m.Endpoints, mirrors...)
	r.Mirrors[host] = m

	return nil
}

// Type returns a string name for the option type
func (r *Registry) Type() string {
	return "list"
}
