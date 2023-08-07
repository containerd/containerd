#!/usr/bin/env bash

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

set -eu -o pipefail

# Rocky Linux doesn't have growpart by default.
(growpart -h > /dev/null) || dnf -y install cloud-utils-growpart

df_line=$(df -T / | grep '^/dev/')
if [[ "$df_line" =~ ^/dev/([a-z]+)([0-9+]) ]]; then
    dev="${BASH_REMATCH[1]}"
    part="${BASH_REMATCH[2]}"
    growpart "/dev/$dev" "$part"

    fstype=$(echo "$df_line" | awk '{print $2}')
    if [[ "$fstype" = 'btrfs' ]]; then
        btrfs filesystem resize max /
    elif [[ "$fstype" = 'xfs' ]]; then
        xfs_growfs -d /
    else
        echo "Unknown filesystem: $df_line"
        exit 1
    fi
elif [[ "$df_line" =~ ^/dev/mapper/ ]]; then
    echo "TODO: support device mapper: $df_line"
else
    echo "Failed to parse: $df_line"
    exit 1
fi
