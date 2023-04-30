# Configure Image Registry

This document describes the method to configure the image registry for `containerd` for use with the `cri` plugin.

> **_NOTE:_** registry.mirrors and registry.configs as previously described in this document
> have been DEPRECATED. As described in [the cri config](./config.md#registry-configuration) you
> should now use the following configuration
```toml
[plugins."io.containerd.grpc.v1.cri".registry]
   config_path = "/etc/containerd/certs.d"
```

## Configure Registry Credentials

> **_NOTE:_**  registry.configs.*.auth is DEPRECATED and will NOT have an equivalent way to store
> unecrypted secrets in the host configuration files. However, it will not be removed until
> a suitable secret management alternative is available as a plugin. It remains supported
> in 1.x releases, including the 1.6 LTS release.

To configure a credential for a specific registry, create/modify the
`/etc/containerd/config.toml` as follows:

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

If you don't already have Google Container Registry (GCR) set-up then you need to do the following steps:

* Create a Google Cloud Platform (GCP) account and project if not already created (see [GCP getting started](https://cloud.google.com/gcp/getting-started))
* Enable GCR for your project (see [Quickstart for Container Registry](https://cloud.google.com/container-registry/docs/quickstart))
* For authentication to GCR: Create [service account and JSON key](https://cloud.google.com/container-registry/docs/advanced-authentication#json-key)
* The JSON key file needs to be downloaded to your system from the GCP console
* For access to the GCR storage: Add service account to the GCR storage bucket with storage admin access rights (see [Granting permissions](https://cloud.google.com/container-registry/docs/access-control#grant-bucket))

Refer to [Pushing and pulling images](https://cloud.google.com/container-registry/docs/pushing-and-pulling) for detailed information on the above steps.

> Note: The JSON key file is a multi-line file and it can be cumbersome to use the contents as a key outside of the file. It is worthwhile generating a single line format output of the file. One way of doing this is using the `jq` tool as follows: `jq -c . key.json`

It is beneficial to first confirm that from your terminal you can authenticate with your GCR and have access to the storage before hooking it into containerd. This can be verified by performing a login to your GCR and
pushing an image to it as follows:

```console
docker login -u _json_key -p "$(cat key.json)" gcr.io

docker pull busybox

docker tag busybox gcr.io/your-gcp-project-id/busybox

docker push gcr.io/your-gcp-project-id/busybox

docker logout gcr.io
```

Now that you know you can access your GCR from your terminal, it is now time to try out containerd.

Edit the containerd config (default location is at `/etc/containerd/config.toml`)
to add your JSON key for `gcr.io` domain image pull
requests:

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
      username = "_json_key"
      password = 'paste output from jq'
```

> Note: `username` of `_json_key` signifies that JSON key authentication will be used.

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
