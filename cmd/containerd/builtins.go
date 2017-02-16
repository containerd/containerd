package main

// register containerd builtins here
import (
	_ "github.com/docker/containerd/services/content"
	_ "github.com/docker/containerd/services/execution"
)
