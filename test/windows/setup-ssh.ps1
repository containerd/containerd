#   Copyright The containerd Authors.

#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at

#       http://www.apache.org/licenses/LICENSE-2.0

#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.

[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$ProgressPreference = 'SilentlyContinue'
$k8sversion = ("v1.16.0-beta.2")

$url = ("https://raw.githubusercontent.com/kubernetes/kubernetes/$k8sversion/cluster/gce/windows/testonly/user-profile.psm1")
Invoke-WebRequest $url -OutFile C:\user-profile.psm1

$url = ("https://raw.githubusercontent.com/kubernetes/kubernetes/$k8sversion/cluster/gce/windows/common.psm1")
Invoke-WebRequest $url -OutFile C:\common.psm1

$url = ("https://raw.githubusercontent.com/kubernetes/kubernetes/$k8sversion/cluster/gce/windows/testonly/install-ssh.psm1")
Invoke-WebRequest $url -OutFile C:\install-ssh.psm1

Import-Module -Force C:\install-ssh.psm1

InstallAndStart-OpenSsh

StartProcess-WriteSshKeys
