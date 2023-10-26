#!/bin/bash

# restart containerd
sudo kill $(pidof containerd)

# Compile
make BUILDTAGS=no_btrfs GODEBUG=true

# Start containerd and download redis
sudo bin/containerd --log-level=debug &
echo sleep for 5 seconds
sleep 5

# List images, remove old image
sudo /usr/local/bin/crictl images list
sudo /usr/local/bin/crictl rmi docker.io/library/redis:alpine
sudo /usr/local/bin/crictl rmi busybox
# Download new image
sudo /usr/local/bin/crictl pull docker.io/library/redis:alpine

ls -alt /var/lib/containerd/tmpoverlayfs/blobs/
