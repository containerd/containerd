
# Registry Configuration - Introduction

Configuring registries will be done by specifying (optionally) a `hosts.toml` file for
each desired registry host in a configuration directory. **Updates under this directory
do not require restarting the containerd daemon.**

## Specifying the Configuration Directory

## Using Host Namespace Configs with CTR

When pulling via `ctr` use the `--hosts-dir` option:
```
ctr images pull --hosts-dir "/etc/containerd/certs.d"
```

## CRI
_The old CRI config pattern for specifying registry.mirrors and registry.configs has
been **DEPRECATED**._ You should now point your registry `config_path` to path where your
`hosts.toml` files are located.

Modify your `config.toml` (default location: `/etc/containerd/config.toml`) as follows:
```toml
[plugins."io.containerd.grpc.v1.cri".registry]
   config_path = "/etc/containerd/certs.d"
```

## Support for Docker's Certificate File Pattern

If no hosts.toml configuration exists in the host directory, it will fallback to check
certificate files based on [Docker's certificate file pattern](https://docs.docker.com/engine/reference/commandline/dockerd/#insecure-registries)
(".crt" files for CA certificates and ".cert"/".key" files for client certificates).

## Registry Host Namespace

A registry host is the location where container images and artifacts are sourced.  These
registry hosts may be local or remote and are typically accessed via http/https using the
[OCI distribution specification](https://github.com/opencontainers/distribution-spec/blob/main/spec.md).
A registry mirror is not a registry host but these mirrors can also be used to pull content.
Registry hosts are typically refered to by their internet domain names, aka. registry
host names. For example, docker.io, quay.io, gcr.io, and ghcr.io.

A registry host namespace is, for the purpose of containerd registry configuration, a
path to the `hosts.toml` file specified by the registry host name, or ip address, and an
optional port identifier. When makeing a pull request for an image the format is
typically as follows:
```
pull [registry_host_name|IP address][:port][/v2][/org_path]<image_name>[:tag|@DIGEST]
```

The registry host namespace portion is `[registry_host_name|IP address][:port]`. Example
tree for docker.io:

```
$ tree /etc/containerd/certs.d
/etc/containerd/certs.d
└── docker.io
    └── hosts.toml
```

The `/v2` portion of the pull request format shown above refers to the version of the
distribution api. If not included in the pull request, `/v2` is added by default for all
clients compliant to the distribution specification linked above.

For example when pulling image_name:tag from a private registry named myregistry.io over
port 5000:
```
pull myregistry.io:5000/image_name:tag
```
The pull will resolve to `https://myregistry.io:5000/v2/image_name:tag`

# Registry Configuration - Examples

### Simple (default) Host Config for Docker
Here is a simple example for a default registry hosts configuration. Set
`config_path = "/etc/containerd/certs.d"` in your config.toml for containerd.
Make a directory tree at the config path that includes `docker.io` as a directory
representing the host namespace to be configured. Then add a `hosts.toml` file
in the `docker.io` to configure the host namespace. It should look like this:

```
$ tree /etc/containerd/certs.d
/etc/containerd/certs.d
└── docker.io
    └── hosts.toml

$ cat /etc/containerd/certs.d/docker.io/hosts.toml
server = "https://docker.io"

[host."https://registry-1.docker.io"]
  capabilities = ["pull", "resolve"]
```

### Setup a Local Mirror for Docker

```
server = "https://registry-1.docker.io"    # Exclude this to not use upstream

[host."https://public-mirror.example.com"]
  capabilities = ["pull"]                  # Requires less trust, won't resolve tag to digest from this host
[host."https://docker-mirror.internal"]
  capabilities = ["pull", "resolve"]
  ca = "docker-mirror.crt"                 # Or absolute path /etc/containerd/certs.d/docker.io/docker-mirror.crt
```

### Bypass TLS Verification Example

To bypass the TLS verification for a private registry at `192.168.31.250:5000`

Create a path and `hosts.toml` text at the path "/etc/containerd/certs.d/docker.io/hosts.toml" with following or similar contents:

```toml
server = "https://registry-1.docker.io"

[host."http://192.168.31.250:5000"]
  capabilities = ["pull", "resolve", "push"]
  skip_verify = true
```

# hosts.toml Content Description - Detail

For each registry host namespace directory in your registry `config_path` you may
include a `hosts.toml` configuration file. The following root level toml fields
apply to the registry host namespace:

*** Note: All paths specified in the `hosts.toml` file may be absolute or relative
to the `hosts.toml` file.

## server field
`server` specifies the default server for this registry host namespace. When
`host`(s) are specified, the hosts are tried first in the order listed.
```
server = "https://docker.io"
```

## capabilities field

`capabilities` is an optional setting for specifying what operations a host is
capable of performing. Include only the values that apply.
```
capabilities =  ["pull", "resolve", "push"]
```

capabilities (or Host capabilities) represent the capabilities of the registry host.
This also represents the set of operations for which the registry host may be trusted
to perform.

For example, pushing is a capability which should only be performed on an upstream
source, not a mirror.

Resolving (the process of converting a name into a digest)
must be considered a trusted operation and only done by
a host which is trusted (or more preferably by secure process
which can prove the provenance of the mapping).

A public mirror should never be trusted to do a resolve action.

| Registry Type    | Pull | Resolve | Push |
|------------------|------|---------|------|
| Public Registry  | yes  | yes     | yes  |
| Private Registry | yes  | yes     | yes  |
| Public Mirror    | yes  | no      | no   |
| Private Mirror   | yes  | yes     | no   |

## ca field

`ca` (Certificate Authority Certification) can be set to a path or an array of
paths each pointing to a ca file for use in authenticating with the registry
namespace.
```
ca = "/etc/certs/mirror.pem"
```
or
```
ca = ["/etc/certs/test-1-ca.pem", "/etc/certs/special.pem"]
```

## client field

`client` certificates are configured as follows

`a path`:
```
client = "/etc/certs/client.pem"
```

`an array of paths`:
```
client = ["/etc/certs/client-1.pem", "/etc/certs/client-2.pem"]
```

`an array of pairs of paths`:
```
client = [["/etc/certs/client.cert", "/etc/certs/client.key"],["/etc/certs/client.pem", ""]]
```

## skip_verify field

`skip_verify` set this flag to `true` to skip the registry certificate
verification for this registry host namespace. (Defaults to `false`)
```
skip_verify = false
```

## header fields (in the toml table format)

`[header]` contains some number of keys where each key is to one of a string or

`an array of strings as follows`:
```
[header]
  x-custom-1 = "custom header"
```

`or`
```
[header]
  x-custom-1 = ["custom header part a","part b"]
```

`or`
```
[header]
  x-custom-1 = "custom header",
  x-custom-1-2 = "another custom header"
```

## host field(s) (in the toml table format)

`[host]."https://namespace"` and `[host].http://namespace` entries in the
`hosts.toml` configuration are registry namespaces used in lieu of the default
registry host namespace. These hosts are sometimes called mirrors because they
may contain a copy of the container images and artifacts you are attempting to
retrieve from the default registry. Each `host`/`mirror` namespace is also
configured in much the same way as the default registry namespace. Notably the
`server` is not specified in the `host` description because it is specified in
the namespace. Here are a few rough examples configuring host mirror namespaces
for this registry host namespace:
```
[host."https://mirror.registry"]
  capabilities = ["pull"]
  ca = "/etc/certs/mirror.pem"
  skip_verify = false
  [host."https://mirror.registry".header]
    x-custom-2 = ["value1", "value2"]

[host."https://mirror-bak.registry/us"]
  capabilities = ["pull"]
  skip_verify = true

[host."http://mirror.registry"]
  capabilities = ["pull"]

[host."https://test-1.registry"]
  capabilities = ["pull", "resolve", "push"]
  ca = ["/etc/certs/test-1-ca.pem", "/etc/certs/special.pem"]
  client = [["/etc/certs/client.cert", "/etc/certs/client.key"],["/etc/certs/client.pem", ""]]

[host."https://test-2.registry"]
  client = "/etc/certs/client.pem"

[host."https://test-3.registry"]
  client = ["/etc/certs/client-1.pem", "/etc/certs/client-2.pem"]
```

**Further, recursion is not supported in the specification of host mirror
namespaces in the hosts.toml file. Thus the following is not allowed/supported:**
```
[host."http://mirror.registry"]
  capabilities = ["pull"]
  [host."http://double-mirror.registry"]
    capabilities = ["pull"]
```
