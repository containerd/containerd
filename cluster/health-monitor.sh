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

set -o nounset
set -o pipefail

# CRICTL is the path of crictl
CRICTL=${CRICTL:-"crictl"}
# INITIAL_WAIT_ATTEMPTS is the number to attempt, before start
# performing health check. The problem is that cri-containerd
# and containerd are started around the same time with health
# monitor, they may not be ready yet when health-monitor is started.
INITIAL_WAIT_ATTEMPTS=${INITIAL_WAIT_ATTEMPTS:-5}
# COMMAND_TIMEOUT is the timeout for the health check command.
COMMAND_TIMEOUT=${COMMAND_TIMEOUT:-60}
# CHECK_PERIOD is the health check period.
CHECK_PERIOD=${CHECK_PERIOD:-10}
# SLEEP_SECONDS is the time to sleep after killing cri-containerd
# and containerd.
SLEEP_SECONDS=${SLEEP_SECONDS:-120}

attempt=1
until timeout ${COMMAND_TIMEOUT} ${CRICTL} pods > /dev/null || (( attempt == INITIAL_WAIT_ATTEMPTS ))
do
  echo "$attempt initial attempt \"$CRICTL pods\"! Trying again in $attempt seconds..."
  sleep $(( attempt++ ))
done

echo "Start performing health check."
while true; do
  # Use crictl pods because it requires both containerd and
  # cri-containerd to be working.
  if ! timeout ${COMMAND_TIMEOUT} ${CRICTL} pods > /dev/null; then
    echo "\"$CRICTL pods\" failed!"
    pkill -x cri-containerd
    pkill -x containerd
    # Wait for a while, as we don't want to kill it again before it is really up.
    sleep ${SLEEP_SECONDS}
  else
    sleep ${CHECK_PERIOD}
  fi
done
