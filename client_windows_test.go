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

package containerd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Microsoft/hcsshim/osversion"
	_ "github.com/containerd/containerd/resources"
)

const (
	defaultAddress = `\\.\pipe\containerd-containerd-test`
)

var (
	defaultRoot  = filepath.Join(os.Getenv("programfiles"), "containerd", "root-test")
	defaultState = filepath.Join(os.Getenv("programfiles"), "containerd", "state-test")
	testImage    string
)

func init() {
	build := osversion.Get()

	bv := uint32(build.Build)
	switch bv {
	case osversion.RS1:
		testImage = "mcr.microsoft.com/windows/nanoserver:sac2016"
	case osversion.RS3:
		testImage = "mcr.microsoft.com/windows/nanoserver:1709"
	case osversion.RS4:
		testImage = "mcr.microsoft.com/windows/nanoserver:1803"
	case osversion.RS5:
		// testImage = "mcr.microsoft.com/windows/nanoserver:1809"
		testImage = "mcr.microsoft.com/windows/nanoserver/insider:10.0.17763.55"
	default:
		panic(fmt.Sprintf("unsupported build (%d) for Windows containers", bv))
	}
}
