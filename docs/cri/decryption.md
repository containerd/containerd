# Configure Image Decryption
This document describes the method to configure encrypted container image decryption for `containerd` for use with the `cri` plugin.

## Encrypted Container Images

Encrypted container images are OCI images which contain encrypted blobs. These encrypted images can be created through the use of [containerd/imgcrypt project](https://github.com/containerd/imgcrypt). To decrypt these images, the `containerd` runtime uses information passed from the `cri` such as keys, options and encryption metadata.

## The "node" Key Model

Encryption ties trust to an entity based on the model in which a key is associated with it. We call this the key model. One such usecase is when we want to tie the trust of a key to the node in a cluster. In this case, we call it the "node" or "host" Key Model. Future work will include more key models to facilitate other trust associations (i.e. for multi-tenancy).

### "node" Key Model Usecase

In this model encryption is tied to worker nodes. The usecase here revolves around the idea that an image should be decryptable only on trusted host. Using this model, various node based technologies which help bootstrap trust in worker nodes and perform secure key distribution (i.e. TPM, host attestation, secure/measured boot). In this scenario, runtimes are capable of fetching the necessary decryption keys. An example of this is using the [`--decryption-keys-path` flag in imgcrypt](https://github.com/containerd/imgcrypt).

### Configuring image decryption for "node" key model

This is the default model since containerd v1.5.

For containerd v1.4, you need to add the following configuration to `/etc/containerd/config.toml` and restart the `containerd` service manually.
```toml
version = 2

[plugins."io.containerd.grpc.v1.cri".image_decryption]
  key_model = "node"

[stream_processors]
  [stream_processors."io.containerd.ocicrypt.decoder.v1.tar.gzip"]
    accepts = ["application/vnd.oci.image.layer.v1.tar+gzip+encrypted"]
    returns = "application/vnd.oci.image.layer.v1.tar+gzip"
    path = "ctd-decoder"
    args = ["--decryption-keys-path", "/etc/containerd/ocicrypt/keys"]
    env= ["OCICRYPT_KEYPROVIDER_CONFIG=/etc/containerd/ocicrypt/ocicrypt_keyprovider.conf"]
  [stream_processors."io.containerd.ocicrypt.decoder.v1.tar"]
    accepts = ["application/vnd.oci.image.layer.v1.tar+encrypted"]
    returns = "application/vnd.oci.image.layer.v1.tar"
    path = "ctd-decoder"
    args = ["--decryption-keys-path", "/etc/containerd/ocicrypt/keys"]
    env= ["OCICRYPT_KEYPROVIDER_CONFIG=/etc/containerd/ocicrypt/ocicrypt_keyprovider.conf"]
```

In this example, container image decryption is set to use the "node" key model.
In addition, the decryption [`stream_processors`](https://github.com/containerd/containerd/blob/main/docs/stream_processors.md) are configured as specified in [containerd/imgcrypt project](https://github.com/containerd/imgcrypt), with the additional field `--decryption-keys-path` configured to specify where decryption keys are located locally in the node.

The `$OCICRYPT_KEYPROVIDER_CONFIG` environment variable is used for [ocicrypt keyprovider protocol](https://github.com/containers/ocicrypt/blob/main/docs/keyprovider.md).
