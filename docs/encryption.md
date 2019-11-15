# Configure Image Encryption
This document describes the method to configure image encryption for `containerd` for use with the `cri` plugin.


## Encrypted Container Images

Encrypted container images are OCI images which contain encrypted blobs. An example of how these encrypted images can be created through the use of [containerd/imgcrypt project](https://github.com/containerd/imgcrypt). In order for the containerd runtime to be able to decrypt these images, the `cri` has to pass the correct options in its calls to containerd. This includes material such as keys and encryption metadata required by the runtime.

## Key Models


Encryption ties trust to an entity based on the model in which a key is associated with it. We call this the key model. There are two currently supported key models in which encrypted containers can be used. These is based on two main usecases. 

1. "node" Key Model - In this model encryption is tied to workers. The usecase here revolves around the idea that an image should be only decryptable only on trusted host. Although the granularity of access is more relaxed (per node), it is beneficial because there various node based technologies which help bootstrap trust in worker nodes and perform secure key distribution (i.e. TPM, host attestation, secure/measured boot). In this scenario, runtimes are capable of fetching the necessary decryption keys. An example of this is using the [`--decryption-keys-path` flag in imgcrypt](https://github.com/containerd/imgcrypt).

2. "multitenant" Key Model - In this model, the trust of encryption is tied to the cluster or users within a cluster. This allows multi-tenancy of users, and is useful in the case where multiple users of kubernetes each want to bring their own encrypted images. This is based on the [KEP that introduces `ImageDecryptSecrets`](https://github.com/kubernetes/enhancements/pull/1066/).


## Configuring image encryption "node" key model

The default configuration does not handle encrypted image. 

In order to set up image encryption, create/modify `/etc/containerd/config.toml` as follows:
```toml
[plugins.cri.image_encryption]
  key_model = "node"

[stream_processors]
  [stream_processors."io.containerd.ocicrypt.decoder.v1.tar.gzip"]
    accepts = ["application/vnd.oci.image.layer.v1.tar+gzip+encrypted"]
    returns = "application/vnd.oci.image.layer.v1.tar+gzip"
    path = "/usr/local/bin/ctd-decoder"
    args = ["--decryption-keys-path", "/keys"]
  [stream_processors."io.containerd.ocicrypt.decoder.v1.tar"]
    accepts = ["application/vnd.oci.image.layer.v1.tar+encrypted"]
    returns = "application/vnd.oci.image.layer.v1.tar"
    path = "/usr/local/bin/ctd-decoder"
    args = ["--decryption-keys-path", "/keys"]
```

This will enable support of `cri` for handling encrypted images. The configuration here sets the key 
model to that of "node". In addition, the decryption `stream_processors` are configured as specified in 
[containerd/imgcrypt project](https://github.com/containerd/imgcrypt), and have an additional field `--decryption-keys-path` 
configured to specify where decryption keys are located locally in the node.

After modify this config, you need restart the `containerd` service.

## Configuring image encryption "multitenant" key model

```toml
[plugins.cri.image_encryption]
  key_model = "multitenant"

[stream_processors]
  [stream_processors."io.containerd.ocicrypt.decoder.v1.tar.gzip"]
    accepts = ["application/vnd.oci.image.layer.v1.tar+gzip+encrypted"]
    returns = "application/vnd.oci.image.layer.v1.tar+gzip"
    path = "/usr/local/bin/ctd-decoder"
  [stream_processors."io.containerd.ocicrypt.decoder.v1.tar"]
    accepts = ["application/vnd.oci.image.layer.v1.tar+encrypted"]
    returns = "application/vnd.oci.image.layer.v1.tar"
    path = "/usr/local/bin/ctd-decoder"
```


To use the multitenant key model, the field should be set to `key_mode = "multitenant"`.
We note that "multitenant" key modle requires a kubernetes version that supports the `ImageDecryptSecrets` 
feature. The `stream_processors` need to be configured to handle decryption as specified in [containerd/imgcrypt project](https://github.com/containerd/imgcrypt). Since keys are passed through
kubernetes, there is no need to specify a way in which the decoder needs to locally obtain keys.

After modify this config, you need restart the `containerd` service.
