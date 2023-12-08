# WIP: containerd with NRI networking resource messages

This is work in progress for containerd with NRI network policy messages added
to pkg/cri/sbserver.

## Added messages

When the pod network is started, 'PreSetupNetwork' and 'PostSetupNetwork'
messages are sent before and after CNI network configuration. 'PreSetupNetwork'
response adds capabilities and args to CNI configuration, while
'PostSetupNetwork' will receive and reply an 'Result' struct which is 1:1 with
'prevResult' Result Type in CNI documentation.

When network configuration is read from disk, all network configurations are
sent to NRI via an 'NetworkConfigurationChanged' message. This is done with a
modified go-cni found with tag 'v0.7.0' at
[https://github.com/pfl/go-cni/tree/nri_network](https://github.com/pfl/go-cni/tree/nri_network)

When a pod network is removed, 'PreNetworkDeleted' is sent to NRI and an empty
result is expected. Once the pod network has been removed, a
'PostNetworkDeleted' event will be sent.

A version of NRI supporting network messages is in the
[nri_network NRI branch](https://github.com/pfl/nri/tree/nri_network).
The tag 'v0.8.0' is pointing to the current work in progress branch.

## Compiling

Compiling containerd will pull in the above required dependency.

## Using

## Bugs

Hopefully not too many.

## Original README

The original [README](/README.md).
