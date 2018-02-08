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

CRICTL_CLI="crictl --runtime-endpoint /var/run/cri-containerd.sock "

crictl_stop_running_containers() {
   for x in $($CRICTL_CLI ps | awk '{ print $1 }' | awk '{if(NR>1)print}') ;do
       $CRICTL_CLI stop $x
   done
}

crictl_rm_stopped_containers() {
   $CRICTL_CLI ps | awk '{ print $1 }' | awk '{if(NR>1)print}'
   for x in $($CRICTL_CLI ps | awk '{ print $1 }' | awk '{if(NR>1)print}') ;do
       $CRICTL_CLI rm $x
   done
}

crictl_stop_pods() {
   for x in $($CRICTL_CLI pods | awk '{ print $1 }' | awk '{if(NR>1)print}') ;do
       $CRICTL_CLI stopp $x
   done
}

crictl_rm_pods() {
   for x in $($CRICTL_CLI pods | awk '{ print $1 }' | awk '{if(NR>1)print}') ;do
       $CRICTL_CLI rmp $x
   done
}

crictl_rm_images() {
   for x in $($CRICTL_CLI images | awk '{ print $1 }' | awk '{if(NR>1)print}') ;do
       $CRICTL_CLI rmi $x
   done
}

command -v crictl >/dev/null 2>&1 || { echo >&2 "crictl not installed. Install from https://github.com/kubernetes-incubator/cri-tools.  Aborting."; exit 2; }
crictl_stop_running_containers
crictl_rm_stopped_containers
crictl_stop_pods
crictl_rm_pods
crictl_rm_images
