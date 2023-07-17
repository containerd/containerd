The containerd managed opt directory provides a way for users to install containerd dependencies using the existing distribution infrastructure.

With runtime v2 and new shim's being built, it is a challenge to
download various shims or runtime dependencies onto a machine.

The managed `/opt` directory for containerd allows users to create images that provide these dependencies and install them on a system using the containerd client API.

Configuration:

*default:* `/opt/containerd`

*containerd config:*
```toml
version = 2

[plugins."io.containerd.internal.v1.opt"]
	path = "/opt/mypath"

```

Usage:

*code:*

```go
image, err := client.Pull(ctx, "docker.io/crosbymichael/runc:latest")
client.Install(ctx, image)
```

Options:

```go
// WithInstallLibs installs libs from the image
func WithInstallLibs(c *InstallConfig) {
}

// WithInstallReplace will replace existing files
func WithInstallReplace(c *InstallConfig) {
}
```

*ctr:*

```bash
ctr content fetch docker.io/crosbymichael/runc:latest
ctr install docker.io/crosbymichael/runc:latest
```

You can manage versions and see what is running via standard image commands.

Images:

These images MUST be small and only contain binaries and libs if required.

```Dockerfile
FROM scratch
Add runc /bin/runc
```

Containerd will only extract files in `/bin` of the image by default, Opts can be added to replace or install `libs/`.
However, we recommend that these binaries be static to reduce linked dependencies.

The code adds a service to manage an `/opt/containerd` directory and
provide that path to callers via the introspection service.

How to Test:

Delete runc from your system.

```bash
> sudo ctr run --rm  docker.io/library/redis:alpine redis
ctr: OCI runtime create failed: unable to retrieve OCI runtime error (open /run/containerd/io.containerd.runtime.v1.linux/default/redis/log.json: no such file or directory): exec: "runc": executable file not found in $PATH: unknown

> sudo ctr content fetch docker.io/crosbymichael/runc:latest
> sudo ctr  install docker.io/crosbymichael/runc:latest

> sudo ctr run --rm  docker.io/library/redis:alpine redis
1:C 01 Aug 15:59:52.864 # oO0OoO0OoO0Oo Redis is starting oO0OoO0OoO0Oo
1:C 01 Aug 15:59:52.864 # Redis version=4.0.10, bits=64, commit=00000000, modified=0, pid=1, just started
1:C 01 Aug 15:59:52.864 # Warning: no config file specified, using the default config. In order to specify a config file use redis-server /path/to/redis.conf
1:M 01 Aug 15:59:52.866 # You requested maxclients of 10000 requiring at least 10032 max file descriptors.
1:M 01 Aug 15:59:52.866 # Server can't set maximum open files to 10032 because of OS error: Operation not permitted.
1:M 01 Aug 15:59:52.866 # Current maximum open files is 1024. maxclients has been reduced to 992 to compensate for low ulimit. If you need higher maxclients increase 'ulimit -n'.
1:M 01 Aug 15:59:52.870 * Running mode=standalone, port=6379.
1:M 01 Aug 15:59:52.870 # WARNING: The TCP backlog setting of 511 cannot be enforced because /proc/sys/net/core/somaxconn is set to the lower value of 128.
1:M 01 Aug 15:59:52.870 # Server initialized
1:M 01 Aug 15:59:52.870 # WARNING overcommit_memory is set to 0! Background save may fail under low memory condition. To fix this issue add 'vm.overcommit_memory = 1' to /etc/sysctl.conf and then reboot or run the command 'sysctl vm.overcommit_memory=1' for this to take effect.
1:M 01 Aug 15:59:52.870 # WARNING you have Transparent Huge Pages (THP) support enabled in your kernel. This will create latency and memory usage issues with Redis. To fix this issue run the command 'echo never > /sys/kernel/mm/transparent_hugepage/enabled' as root, and add it to your /etc/rc.local in order to retain the setting after a reboot. Redis must be restarted after THP is disabled.
1:M 01 Aug 15:59:52.870 * Ready to accept connections
^C1:signal-handler (1533139193) Received SIGINT scheduling shutdown...
1:M 01 Aug 15:59:53.472 # User requested shutdown...
1:M 01 Aug 15:59:53.472 * Saving the final RDB snapshot before exiting.
1:M 01 Aug 15:59:53.484 * DB saved on disk
1:M 01 Aug 15:59:53.484 # Redis is now ready to exit, bye bye...
```
For Windows:

```Dockerfile
FROM mcr.microsoft.com/windows/nanoserver:1809
ADD runhcs.exe /bin/runhcs.exe
```

```powershell
> ctr content fetch docker.io/ameyagawde/runhcs:1809 #An example image, not supported by containerd
> ctr install docker.io/ameyagawde/runhcs:1809
```
The Windows equivalent for `/opt/containerd` will be `$env:ProgramData\containerd\root\opt`