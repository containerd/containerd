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

package client

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Microsoft/hcsshim/osversion"
	_ "github.com/Microsoft/hcsshim/test/functional/manifest" // For rsrc_amd64.syso
)

const (
	defaultAddress = `\\.\pipe\containerd-containerd-test`
)

var (
	defaultRoot           = filepath.Join(os.Getenv("programfiles"), "containerd", "root-test")
	defaultState          = filepath.Join(os.Getenv("programfiles"), "containerd", "state-test")
	testImage             string
	testMultiLayeredImage = "ghcr.io/containerd/volume-copy-up:2.1"
	shortCommand          = withTrue()
	longCommand           = withProcessArgs("ping", "-t", "localhost")
)

func init() {
	b := osversion.Build()
	switch b {
	case osversion.RS1:
		testImage = "mcr.microsoft.com/windows/nanoserver:sac2016"
	case osversion.RS3:
		testImage = "mcr.microsoft.com/windows/nanoserver:1709"
	case osversion.RS4:
		testImage = "mcr.microsoft.com/windows/nanoserver:1803"
	case osversion.RS5:
		testImage = "mcr.microsoft.com/windows/nanoserver:1809"
	case osversion.V19H1:
		testImage = "mcr.microsoft.com/windows/nanoserver:1903"
	case osversion.V19H2:
		testImage = "mcr.microsoft.com/windows/nanoserver:1909"
	case osversion.V20H1:
		testImage = "mcr.microsoft.com/windows/nanoserver:2004"
	case osversion.V20H2:
		testImage = "mcr.microsoft.com/windows/nanoserver:20H2"
	case osversion.V21H2Server:
		testImage = "mcr.microsoft.com/windows/nanoserver:ltsc2022"
	default:
		// Due to some efforts in improving down-level compatibility for Windows containers (see
		// https://techcommunity.microsoft.com/t5/containers/windows-server-2022-and-beyond-for-containers/ba-p/2712487)
		// the ltsc2022 image should continue to work on builds ws2022 and onwards (Windows 11 for example). With this in mind,
		// if there's no mapping for the host build just use the Windows Server 2022 image.
		if b > osversion.V21H2Server {
			testImage = "mcr.microsoft.com/windows/nanoserver:ltsc2022"
			return
		}
		fmt.Println("No test image defined for Windows build version:", b)
		panic("No windows test image found for this Windows build")
	}

	fmt.Println("Windows test image:", testImage, ", Windows build version:", b)
}
