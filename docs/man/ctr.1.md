# ctr 1 01/30/2018

## SYNOPSIS

**ctr [global options] command [command options] [arguments...]**

## DESCRIPTION

**ctr** is an unsupported debug and administrative client for interacting
with the containerd daemon. Because it is unsupported, the commands,
options, and operation are not guaranteed to be backward compatible or
stable from release to release of the containerd project.

## OPTIONS

The following commands are available in the **ctr** utility:

**plugins,plugin**
: Provides information about containerd plugins

**version**
: Prints the client and server versions

**containers,c,container**
: Manages and interacts with containers

**content**
: Manages and interacts with content

**events,event**
: Displays containerd events

**images,image**
: Manages and interacts with images

**namespaces,namespace**
: Manages and interacts with containerd namespaces

**pprof**
: Provides golang pprof outputs for containerd

**run**
: Runs a container

**snapshots,snapshot**
: Manages and interacts with snapshots

**tasks,t,task**
: Manages and interacts with tasks

**shim**
: Interacts with a containerd shim directly

**help,h**
: Displays a list of commands or help for one specific command

The following global options apply to all **ctr** commands:

**--debug**
: Enable debug output in logs

**--address value, -a value**
: Address for containerd's GRPC server (default: */run/containerd/containerd.sock*)

**--timeout value**
: Total timeout for ctr commands (default: *0s*)

**--connect-timeout value**
: Timeout for connecting to containerd (default: *0s*)

**--namespace value, -n value**
: Namespace to use with commands (default: *default*) [also read from *$CONTAINERD_NAMESPACE*]

**--help, -h**
: Show help text

**--version, -v**
: Prints the **ctr** version

## BUGS

Note that the **ctr** utility is not an officially supported part of the
containerd project releases.

However, please feel free to file any specific issues that you encounter at
https://github.com/containerd/containerd.

## AUTHOR

Phil Estes <estesp@gmail.com>

## SEE ALSO

containerd(1), config.toml(5), containerd-config(1)
