# containerd-config 1 01/30/2018

## SYNOPSIS

containerd config [command]

## DESCRIPTION

The *containerd config* command has one subcommand, named *default*, which
will display on standard output the default containerd config for this version
of the containerd daemon.

This output can be piped to a __config.toml(5)__ file and placed in
**/etc/containerd** to be used as the configuration for containerd on daemon
startup. The configuration can be placed in any filesystem location and used
with the **--config** option to the containerd daemon as well.

See __config.toml(5)__ for more information on the containerd configuration
options.

## OPTIONS

**default**
: This subcommand will output the TOML formatted containerd configuration to standard output

## BUGS

Please file any specific issues that you encounter at
https://github.com/containerd/containerd.

## AUTHOR

Phil Estes <estesp@gmail.com>

## SEE ALSO

ctr(1), config.toml(5), containerd(1)
