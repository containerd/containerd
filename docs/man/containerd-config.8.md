# containerd-config 8 07/25/2026

## NAME

containerd-config - information on the containerd config

## SYNOPSIS

containerd config [command]

## DESCRIPTION

The *containerd config* command prints configuration for the containerd
daemon. Plugin options depend on which plugins are compiled into the binary,
so the subcommands below are the authoritative way to list defaults for a
given install.

See __containerd-config.toml(5)__ for global settings and the plugin
configuration model. Documentation: https://containerd.io/docs/

## OPTIONS

**default**
: Print the complete default TOML configuration for this containerd binary,
including every compiled-in plugin. Use this to discover configuration keys:

```
containerd config default > /etc/containerd/config.toml
```

**dump**
: Load the active configuration file (default **/etc/containerd/config.toml**,
or the path from **--config** / **-c**), apply **imports**, and print the
merged configuration.

**migrate**
: Currently an alias of **dump**. Both load the active configuration and
print it at the latest supported config version. Does not rewrite files
listed under **imports**.

## BUGS

Please file any specific issues that you encounter at
https://github.com/containerd/containerd.

## AUTHOR

Phil Estes <estesp@gmail.com>

## SEE ALSO

ctr(8), containerd(8), containerd-config.toml(5)

https://containerd.io/docs/
