#!/bin/bash

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

crictl_stop_sandboxes() {
   for x in $($CRICTL_CLI sandboxes | awk '{ print $1 }' | awk '{if(NR>1)print}') ;do    
       $CRICTL_CLI stops $x
   done
}

crictl_rm_sandboxes() {
   for x in $($CRICTL_CLI sandboxes | awk '{ print $1 }' | awk '{if(NR>1)print}') ;do
       $CRICTL_CLI rms $x
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
crictl_stop_sandboxes
crictl_rm_sandboxes
crictl_rm_images
