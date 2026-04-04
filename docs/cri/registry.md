# Configure Image Registry

This document describes the method to configure the image registry for `containerd` for use with the `cri` plugin.

> **_NOTE:_** registry.mirrors and registry.configs as previously described in this document
> have been DEPRECATED. As described in [the cri config](./config.md#registry-configuration) you
> should now use the following configuration
+ In containerd 2.x
```toml
[plugins."io.containerd.cri.v1.images".registry]
   config_path = "/etc/containerd/certs.d"
```
+ In containerd 1.x
```toml
[plugins."io.containerd.grpc.v1.cri".registry]
   config_path = "/etc/containerd/certs.d"
```

If no registry-specific options are set, `config_path` will default to
`/etc/containerd/certs.d:/etc/docker/certs.d`, which enables compatibility with
[the docker method of adding registry configuration](https://docs.docker.com/registry/insecure/#use-self-signed-certificates).

## Configure Registry Credentials

> **_NOTE:_**  registry.configs.*.auth is DEPRECATED and will NOT have an equivalent way to store
> unencrypted secrets in the host configuration files. However, it will not be removed until
> a suitable secret management alternative is available as a plugin. It remains supported
> in 1.x releases, including the 1.6 LTS release.

To configure a credential for a specific registry, create/modify the
`/etc/containerd/config.toml` as follows:

+ In containerd 2.x
```toml
# explicitly use v3 config format
version = 3

# The registry host has to be a domain name or IP. Port number is also
# needed if the default HTTPS or HTTP port is not used.
[plugins."io.containerd.cri.v1.images".registry.configs."gcr.io".auth]
  username = ""
  password = ""
  auth = ""
  identitytoken = ""
```
+ In containerd 1.x
```toml
# explicitly use v2 config format
version = 2

# The registry host has to be a domain name or IP. Port number is also
# needed if the default HTTPS or HTTP port is not used.
[plugins."io.containerd.grpc.v1.cri".registry.configs."gcr.io".auth]
  username = ""
  password = ""
  auth = ""
  identitytoken = ""
```

The meaning of each field is the same with the corresponding field in `.docker/config.json`.

Please note that auth config passed by CRI takes precedence over this config.
The registry credential in this config will only be used when auth config is
not specified by Kubernetes via CRI.

After modifying this config, you need to restart the `containerd` service.

### Configure Registry Credentials Example - GCR with Service Account Key Authentication

If you don't already have Google Artifact Registry set up then you need to do the following steps:

* Create a Google Cloud Platform (GCP) account and project if not already created (see [GCP getting started](https://cloud.google.com/gcp/getting-started))
* Enable Artifact Registry for your project (see [Guide for Artifact Registry](https://docs.cloud.google.com/artifact-registry/docs/enable-service))
* For authentication to Artifact Registry: Choose an [authentication method](https://docs.cloud.google.com/artifact-registry/docs/docker/authentication)

Refer to [Pushing and pulling images](https://docs.cloud.google.com/artifact-registry/docs/docker/pushing-and-pulling) for detailed information on the above steps.

Confirm using the steps on the above documentation page for Artifact Registry that from your terminal you can authenticate and interact with images in your registry.

Now that you know you can access your Artifact Registry from your terminal, it is now time to try out containerd. Use the authentication guide above to create a service account key and download the JSON locally.

Edit the containerd config (default location is at `/etc/containerd/config.toml`)
to add your service account key for `gcr.io` domain image pull requests:

+ In containerd 2.x
```toml
version = 3

[plugins."io.containerd.cri.v1.images".registry]
  [plugins."io.containerd.cri.v1.images".registry.mirrors]
    [plugins."io.containerd.cri.v1.images".registry.mirrors."docker.io"]
      endpoint = ["https://registry-1.docker.io"]
    [plugins."io.containerd.cri.v1.images".registry.mirrors."gcr.io"]
      endpoint = ["https://gcr.io"]
  [plugins."io.containerd.cri.v1.images".registry.configs]
    [plugins."io.containerd.cri.v1.images".registry.configs."gcr.io".auth]
      username = "_json_key_base64"
      password = 'paste output from jq'
```
+ In containerd 1.x
```toml
version = 2

[plugins."io.containerd.grpc.v1.cri".registry]
  [plugins."io.containerd.grpc.v1.cri".registry.mirrors]
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors."docker.io"]
      endpoint = ["https://registry-1.docker.io"]
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors."gcr.io"]
      endpoint = ["https://gcr.io"]
  [plugins."io.containerd.grpc.v1.cri".registry.configs]
    [plugins."io.containerd.grpc.v1.cri".registry.configs."gcr.io".auth]
      username = "_json_key_base64"
      password = 'paste output from jq'
```

> Note: `username` of `_json_key_base64` signifies that JSON key authentication will be used.

Restart containerd:

```console
service containerd restart
```

Pull an image from your GCR with `crictl`:

```console
$ sudo crictl pull gcr.io/your-gcp-project-id/busybox

DEBU[0000] get image connection
DEBU[0000] connect using endpoint 'unix:///run/containerd/containerd.sock' with '3s' timeout
DEBU[0000] connected successfully using endpoint: unix:///run/containerd/containerd.sock
DEBU[0000] PullImageRequest: &PullImageRequest{Image:&ImageSpec{Image:gcr.io/your-gcr-instance-id/busybox,},Auth:nil,SandboxConfig:nil,}
DEBU[0001] PullImageResponse: &PullImageResponse{ImageRef:sha256:78096d0a54788961ca68393e5f8038704b97d8af374249dc5c8faec1b8045e42,}
Image is up to date for sha256:78096d0a54788961ca68393e5f8038704b97d8af374249dc5c8faec1b8045e42
```

---

NOTE: The configuration syntax used in this doc is in version 2 which is the recommended since `containerd` 1.3. For the previous config format you can reference [https://github.com/containerd/cri/blob/release/1.2/docs/registry.md](https://github.com/containerd/cri/blob/release/1.2/docs/registry.md).
