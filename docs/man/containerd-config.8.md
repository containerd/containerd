# containerd-config 8 07/23/2026

## NAME

containerd-config - information on the containerd config

## SYNOPSIS

containerd config [command]

## DESCRIPTION

The *containerd config* command inspects and generates configuration for the
containerd daemon. Because containerd loads plugins dynamically, the set of
available configuration options depends on the plugins compiled into the
running binary. The subcommands below are the authoritative way to discover
the full default configuration for a given build.

See __containerd-config.toml(5)__ for a description of global settings and the
plugin configuration model. Topic guides are published at
https://containerd.io/docs/.

## OPTIONS

**default**
: Output the complete default TOML configuration for this version of
containerd, including defaults for every plugin compiled into the binary.
This is the primary discovery tool when looking for a configuration key.
Pipe the output to a file to create a starting config, for example:

```
containerd config default > /etc/containerd/config.toml
```

**dump**
: Load the active configuration file (default **/etc/containerd/config.toml**,
or the path given by **--config** / **-c**), apply any **imports**, and print
the fully merged configuration that the daemon would use. Useful for verifying
overrides and included fragments.

**migrate**
: Load the active configuration file and print it migrated to the latest
supported config version. Does not rewrite subconfig files listed under
**imports**. Review the output before replacing the on-disk config.

## BUGS

Please file any specific issues that you encounter at
https://github.com/containerd/containerd.

## AUTHOR

Phil Estes <estesp@gmail.com>

## SEE ALSO

ctr(8), containerd(8), containerd-config.toml(5)

Online documentation: https://containerd.io/docs/
