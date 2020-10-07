#!/bin/bash

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

set -o errexit
set -o nounset
set -o pipefail

source $(dirname "${BASH_SOURCE[0]}")/utils.sh
cd ${ROOT}

echo "Compare vendor with containerd vendors..."
containerd_vendor=$(mktemp /tmp/containerd-vendor.conf.XXXX)
from-vendor CONTAINERD github.com/containerd/containerd
curl -s https://raw.githubusercontent.com/${CONTAINERD_REPO#*/}/${CONTAINERD_VERSION}/vendor.conf > ${containerd_vendor}
# Create a temporary vendor file to update.
tmp_vendor=$(mktemp /tmp/vendor.conf.XXXX)
while read vendor; do
  repo=$(echo ${vendor} | awk '{print $1}')
  commit=$(echo ${vendor} | awk '{print $2}')
  alias=$(echo ${vendor} | awk '{print $3}')
  vendor_in_containerd=$(grep ${repo} ${containerd_vendor} || true)
  if [ -z "${vendor_in_containerd}" ]; then
    echo ${vendor} >> ${tmp_vendor}
    continue
  fi
  commit_in_containerd=$(echo ${vendor_in_containerd} | awk '{print $2}')
  alias_in_containerd=$(echo ${vendor_in_containerd} | awk '{print $3}')
  if [[ "${commit}" != "${commit_in_containerd}" || "${alias}" != "${alias_in_containerd}" ]]; then
    echo ${vendor_in_containerd} >> ${tmp_vendor}
  else
    echo ${vendor} >> ${tmp_vendor}
  fi
done < vendor.conf
# Update vendors if temporary vendor.conf is different from the original one.
if ! diff vendor.conf ${tmp_vendor} > /dev/null; then
  if [ $# -gt 0 ] && [ ${1} = "-only-verify" ]; then
    echo "Need to update vendor.conf."
    diff vendor.conf ${tmp_vendor}
    rm ${tmp_vendor}
    exit 1
  else
    echo "Updating vendor.conf."
    mv ${tmp_vendor} vendor.conf
  fi
fi
rm ${containerd_vendor}
