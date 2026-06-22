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
[plugins."io.containerd.cri.v1.images".registry.configs."us-central1-docker.pkg.dev".auth]
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
[plugins."io.containerd.grpc.v1.cri".registry.configs."us-central1-docker.pkg.dev".auth]
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

### Configure Registry Credentials Example - Google Artifact Registry with Service Account Key Authentication

If you don't already have Google Artifact Registry set up then you need to do the following steps:

* Create a Google Cloud Platform (GCP) account and project if not already created (see [GCP getting started](https://docs.cloud.google.com/docs/get-started/))
* Enable the Artifact Registry API and create a Docker repository (see [Quickstart for Artifact Registry](https://docs.cloud.google.com/artifact-registry/docs/docker/store-docker-container-images))
* For authentication to Artifact Registry: Create a [service account and JSON key](https://docs.cloud.google.com/artifact-registry/docs/repositories/configure-remote-authentication#json-key)
* The JSON key file needs to be downloaded to your system from the GCP console
* Grant the service account appropriate permissions to access your Artifact Registry repository (see [Granting permissions](https://docs.cloud.google.com/artifact-registry/docs/access-control))

Refer to [Pushing and pulling images](https://docs.cloud.google.com/artifact-registry/docs/docker/pushing-and-pulling) for detailed information on the above steps.

> Note: The JSON key file is a multi-line file. When using the key file directly, use `_json_key` as the Docker username. If you base64-encode the key file contents, use `_json_key_base64` instead.

It is beneficial to first confirm that from your terminal you can authenticate with your Google Artifact Registry repository and have access to it before hooking it into containerd. This can be verified by performing a login to your Google Artifact Registry and pushing an image to it as follows:

```console
cat key.json | docker login -u _json_key --password-stdin us-central1-docker.pkg.dev

docker pull busybox

docker tag busybox us-central1-docker.pkg.dev/your-gcp-project-id/your-repository/busybox

docker push us-central1-docker.pkg.dev/your-gcp-project-id/your-repository/busybox

docker logout us-central1-docker.pkg.dev
```

Now that you know you can access your Artifact Registry repository from your terminal, it is now time to try out containerd.

Edit the containerd config (default location is at `/etc/containerd/config.toml`)
to add your JSON key for `us-central1-docker.pkg.dev` domain image pull
requests:
+ In containerd 2.x
```toml
version = 3

[plugins."io.containerd.cri.v1.images".registry]
  [plugins."io.containerd.cri.v1.images".registry.mirrors]
    [plugins."io.containerd.cri.v1.images".registry.mirrors."docker.io"]
      endpoint = ["https://registry-1.docker.io"]
    [plugins."io.containerd.cri.v1.images".registry.mirrors."us-central1-docker.pkg.dev"]
      endpoint = ["https://us-central1-docker.pkg.dev"]
  [plugins."io.containerd.cri.v1.images".registry.configs]
    [plugins."io.containerd.cri.v1.images".registry.configs."us-central1-docker.pkg.dev".auth]
      username = "_json_key"
      password = 'paste output from jq -c . key.json'
```
+ In containerd 1.x
```toml
version = 2

[plugins."io.containerd.grpc.v1.cri".registry]
  [plugins."io.containerd.grpc.v1.cri".registry.mirrors]
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors."docker.io"]
      endpoint = ["https://registry-1.docker.io"]
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors."us-central1-docker.pkg.dev"]
      endpoint = ["https://us-central1-docker.pkg.dev"]
  [plugins."io.containerd.grpc.v1.cri".registry.configs]
    [plugins."io.containerd.grpc.v1.cri".registry.configs."us-central1-docker.pkg.dev".auth]
      username = "_json_key"
      password = 'paste output from jq -c . key.json'
```

> Note: Setting username to `_json_key` signifies that service account JSON key authentication will be used. If using a base64-encoded JSON key instead, use `_json_key_base64`.

Restart containerd:

```console
service containerd restart
```

Pull an image from your Artifact Registry repository with `crictl`:

```console
$ sudo crictl pull us-central1-docker.pkg.dev/your-gcp-project-id/your-repository/busybox

DEBU[0000] get image connection
DEBU[0000] connect using endpoint 'unix:///run/containerd/containerd.sock' with '3s' timeout
DEBU[0000] connected successfully using endpoint: unix:///run/containerd/containerd.sock
DEBU[0000] PullImageRequest: &PullImageRequest{Image:&ImageSpec{Image:us-central1-docker.pkg.dev/your-gcp-project-id/your-repository/busybox,},Auth:nil,SandboxConfig:nil,}
DEBU[0001] PullImageResponse: &PullImageResponse{ImageRef:sha256:78096d0a54788961ca68393e5f8038704b97d8af374249dc5c8faec1b8045e42,}
Image is up to date for sha256:78096d0a54788961ca68393e5f8038704b97d8af374249dc5c8faec1b8045e42
```

---

NOTE: The configuration syntax used in this doc is in version 2 which is the recommended since `containerd` 1.3. For the previous config format you can reference [https://github.com/containerd/cri/blob/release/1.2/docs/registry.md](https://github.com/containerd/cri/blob/release/1.2/docs/registry.md).
