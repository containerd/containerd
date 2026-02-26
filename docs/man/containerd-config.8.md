# containerd-config 8 01/30/2018

## NAME

containerd-config - information on the containerd config

## SYNOPSIS

containerd config [command]

## DESCRIPTION

The _containerd config_ command is responsible for managing the configuration of containerd.

## OPTIONS

**default**
: This subcommand will output the default TOML formatted containerd configuration to standard output

This output can be piped to a **containerd-config.toml(5)** file and placed in
**/etc/containerd** to be used as the configuration for containerd on daemon
startup. The configuration can be placed in any filesystem location and used
with the **--config** option to the containerd daemon as well.

See **containerd-config.toml(5)** for more information on the containerd
configuration options.

**dump**
: This subcommand will output all the current containerd configuration considering all the subconfig files to standard output

**migrate**
: Get the current containerd configuration, and without considering subconfig files, migrate it to latest version config and show it in standard output

**help**, **h**
: Shows a list of commands or help for one command

## BUGS

Please file any specific issues that you encounter at
https://github.com/containerd/containerd.

## AUTHOR

Phil Estes <estesp@gmail.com>

## SEE ALSO

ctr(8), containerd(8), containerd-config.toml(5)
