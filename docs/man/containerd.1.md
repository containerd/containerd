# containerd 1 01/29/2018

## SYNOPSIS

containerd [global options] command [command options] [arguments...]

## DESCRIPTION

**containerd** is a high performance container runtime whose daemon can be started
by using this command. If none of the *config*, *publish*, or *help* commands
are specified the default action of the **containerd** command is to start the
containerd daemon in the foreground.

A default configuration is used if no TOML configuration is specified or located
at the default file location. The *containerd config* command can be used to
generate the default configuration for containerd. The output of that command
can be used and modified as necessary as a custom configuration.

The *publish* command is used internally by parts of the containerd runtime
to publish events. It is not meant to be used as a standalone utility.

## OPTIONS

**--config value, -c value**
: Specify the default path to the configuration file (default: "/etc/containerd/config.toml")

**--log-level value, -l value**
: Set the logging level. Available levels are: [debug, info, warn, error, fatal, panic]

**--address value, -a value**
: UNIX socket address for containerd's GRPC server to listen on (default: "/run/containerd/containerd.sock")

**--root value**
: The containerd root directory (default: "/var/lib/containerd"). A persistent directory location where metadata and image content are stored

**--state value**
: The containerd state directory (default: "/run/containerd"). A transient state directory used during containerd operation

**--help, -h**
: Show containerd command help text

**--version, -v**
: Print the containerd server version

## BUGS

Please file any specific issues that you encounter at
https://github.com/containerd/containerd.

## AUTHOR

Phil Estes <estesp@gmail.com>

## SEE ALSO

ctr(1), config.toml(5), containerd-config(1)
