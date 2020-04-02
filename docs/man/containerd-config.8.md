# containerd-config 8 01/30/2018

## NAME

containerd-config - information on the containerd config

## SYNOPSIS

containerd config [command]

## DESCRIPTION

The *containerd config* command has one subcommand, named *default*, which
will display on standard output the default containerd config for this version
of the containerd daemon.

This output can be piped to a __containerd-config.toml(5)__ file and placed in
**/etc/containerd** to be used as the configuration for containerd on daemon
startup. The configuration can be placed in any filesystem location and used
with the **--config** option to the containerd daemon as well.

See __containerd-config.toml(5)__ for more information on the containerd
configuration options.

## OPTIONS

**default**
: This subcommand will output the TOML formatted containerd configuration to standard output

## BUGS

Please file any specific issues that you encounter at
https://github.com/containerd/containerd.

## AUTHOR

Phil Estes <estesp@gmail.com>

## SEE ALSO

ctr(8), containerd(8), containerd-config.toml(5)
