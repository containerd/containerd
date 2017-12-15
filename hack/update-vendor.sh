#!/bin/bash

# Copyright 2017 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

ROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"/..
cd ${ROOT}

echo "Sort vendor.conf..."
sort vendor.conf -o vendor.conf

echo "Compare vendor with hack/versions..."
need_update=false
declare -A map=()
map["RUNC_VERSION"]="github.com/opencontainers/runc"
map["CNI_VERSION"]="github.com/containernetworking/cni"
map["CONTAINERD_VERSION"]="github.com/containerd/containerd"
map["KUBERNETES_VERSION"]="k8s.io/kubernetes"
for key in ${!map[@]}
do
  vendor_commitid=$(grep ${map[${key}]} vendor.conf | awk '{print $2}')
  version_commitid=$(grep ${key} hack/versions | awk -F "=" '{print $2}')
  if [ ${vendor_commitid} != ${version_commitid} ]; then
    if [ $# -gt 0 ] && [ ${1} = "-only-verify" ]; then
      need_update=true
      echo "Need to update the value of ${key} from ${version_commitid} to ${vendor_commitid}."
    else
      echo "Updating the value of ${key} from ${version_commitid} to ${vendor_commitid}."
      sed -i "s/${version_commitid}/${vendor_commitid}/g" hack/versions
    fi
  fi
done

if [ ${need_update} = true ]; then
  echo "Please update \"hack/versions\" by executing \"hack/update-vendor.sh\"!"
  exit 1
fi

echo "Please commit the change made by this file..."
