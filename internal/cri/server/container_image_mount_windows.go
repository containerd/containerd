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
	"path/filepath"

	"golang.org/x/sys/windows"
)

func toFullPath(path string) (string, error) {
	p, err := toShort(path)
	if err != nil {
		return "", err
	}
	p, err = toLong(p)
	if err != nil {
		return "", err
	}
	// windows.GetLongPathName does not change the case of the drive letter,
	// but the result of EvalSymlinks must be unique, so we have
	// EvalSymlinks(`c:\a`) == EvalSymlinks(`C:\a`).
	// Make drive letter upper case.
	if len(p) >= 2 && p[1] == ':' && 'a' <= p[0] && p[0] <= 'z' {
		p = string(p[0]+'A'-'a') + p[1:]
	} else if len(p) >= 6 && p[5] == ':' && 'a' <= p[4] && p[4] <= 'z' {
		p = p[:3] + string(p[4]+'A'-'a') + p[5:]
	}
	return filepath.Clean(p), nil
}

func toShort(path string) (string, error) {
	p, err := windows.UTF16FromString(path)
	if err != nil {
		return "", err
	}
	b := p // GetShortPathName says we can reuse buffer
	n, err := windows.GetShortPathName(&p[0], &b[0], uint32(len(b)))
	if err != nil {
		return "", err
	}
	if n > uint32(len(b)) {
		b = make([]uint16, n)
		if _, err = windows.GetShortPathName(&p[0], &b[0], uint32(len(b))); err != nil {
			return "", err
		}
	}
	return windows.UTF16ToString(b), nil
}

func toLong(path string) (string, error) {
	p, err := windows.UTF16FromString(path)
	if err != nil {
		return "", err
	}
	b := p // GetLongPathName says we can reuse buffer
	n, err := windows.GetLongPathName(&p[0], &b[0], uint32(len(b)))
	if err != nil {
		return "", err
	}
	if n > uint32(len(b)) {
		b = make([]uint16, n)
		n, err = windows.GetLongPathName(&p[0], &b[0], uint32(len(b)))
		if err != nil {
			return "", err
		}
	}
	b = b[:n]
	return windows.UTF16ToString(b), nil
}
