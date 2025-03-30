# Content Flow

A major goal of containerd is to create a system wherein content can be used for executing containers.
In order to execute on that flow, containerd requires content and to manage it.

This document describes how content flows into containerd, how it is managed, and where it exists
at each stage in the process. We use an example of going from a known image
[docker.io/library/redis:5.0.9](https://hub.docker.com/layers/library/redis/5.0.9/images/sha256-9bb13890319dc01e5f8a4d3d0c4c72685654d682d568350fd38a02b1d70aee6b) to explore the
flow of content.

## Content Areas

Content exists in several areas in the containerd lifecycle:

* OCI registry, for example [hub.docker.com](https://hub.docker.com) or [quay.io](https://quay.io)
* containerd content store, under containerd's local storage space, for example, on a standard Linux installation at `/var/lib/containerd/io.containerd.content.v1.content`
* snapshots, under containerd's local storage space, for example, on a standard Linux installation at `/var/lib/containerd/io.containerd.snapshotter.v1.<type>`. For an overlayfs snapshotter, that would be at `/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs`

A container needs a mountable, and often mutable, filesystem to run. This filesystem is created from the content in the content store.
In order to create a container, the following must occur:

1. The image and all its content must be loaded into the local content store. This normally happens via download from the OCI registry, but you can load content in directly as well. This content is in the same format as in the registry.
1. The layers from the image must be read and applied to a filesystem, creating what is known as a "committed snapshot". This is repeated for each layer in order. This process is known as "unpacking".
1. A final mutable mountable filesystem, an "active snapshot", must be created on top of the final layer of content for the image.

A container now can be created, with its root filesystem as the active snapshot.

The rest of this document looks at the content in each area in detail, and how they relate to one another.

### Image Format

Images in a registry normally are stored in the following format. An "image" is comprised of a JSON document
known as a descriptor. A descriptor always contains an element, `mediaType`, which tells us which type it is. It is one of two options:

* a "manifest", which lists the hashes of the config file for running the image as a container, and the binary data layers that create the filesystem for the image
* an "index", which lists the hashes of manifests, one per platform, where a platform is a combination of architecture (e.g. amd64 or arm64) and operating system (e.g. linux)

The purpose of an index is to allow us to pick which manifest matches our target platform.

To convert an image reference, such as `redis:5.0.9`, from a registry to actual on-disk storage, we:

1. Retrieve the descriptor (JSON document) for the image
1. Determine from the `mediaType` if the descriptor is a manifest or an index:
   * If the descriptor is an index, find in it the platform (architecture+os) that represents the platform on which we want to run the container, use that hash to retrieve the manifest
   * If the descriptor already is a manifest, continue
1. For each element in the manifest - the config and one or more layers - use the hash listed to retrieve the components and save them

We use our example image, `redis:5.0.9`, to clarify the process.

When we first resolve `redis:5.0.9`, we get the following JSON document:

```json
{
    "manifests": [
        {
            "digest": "sha256:9bb13890319dc01e5f8a4d3d0c4c72685654d682d568350fd38a02b1d70aee6b",
            "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
            "platform": {
                "architecture": "amd64",
                "os": "linux"
            },
            "size": 1572
        },
        {
            "digest": "sha256:aeb53f8db8c94d2cd63ca860d635af4307967aa11a2fdead98ae0ab3a329f470",
            "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
            "platform": {
                "architecture": "arm",
                "os": "linux",
                "variant": "v5"
            },
            "size": 1573
        },
        {
            "digest": "sha256:17dc42e40d4af0a9e84c738313109f3a95e598081beef6c18a05abb57337aa5d",
            "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
            "platform": {
                "architecture": "arm",
                "os": "linux",
                "variant": "v7"
            },
            "size": 1573
        },
        {
            "digest": "sha256:613f4797d2b6653634291a990f3e32378c7cfe3cdd439567b26ca340b8946013",
            "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
            "platform": {
                "architecture": "arm64",
                "os": "linux",
                "variant": "v8"
            },
            "size": 1573
        },
        {
            "digest": "sha256:ee0e1f8d8d338c9506b0e487ce6c2c41f931d1e130acd60dc7794c3a246eb59e",
            "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
            "platform": {
                "architecture": "386",
                "os": "linux"
            },
            "size": 1572
        },
        {
            "digest": "sha256:1072145f8eea186dcedb6b377b9969d121a00e65ae6c20e9cd631483178ea7ed",
            "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
            "platform": {
                "architecture": "mips64le",
                "os": "linux"
            },
            "size": 1572
        },
        {
            "digest": "sha256:4b7860fcaea5b9bbd6249c10a3dc02a5b9fb339e8aef17a542d6126a6af84d96",
            "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
            "platform": {
                "architecture": "ppc64le",
                "os": "linux"
            },
            "size": 1573
        },
        {
            "digest": "sha256:d66dfc869b619cd6da5b5ae9d7b1cbab44c134b31d458de07f7d580a84b63f69",
            "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
            "platform": {
                "architecture": "s390x",
                "os": "linux"
            },
            "size": 1573
        }
    ],
    "mediaType": "application/vnd.docker.distribution.manifest.list.v2+json",
    "schemaVersion": 2
}
```

The descriptor above, towards the end, shows that the `mediaType` is a "manifest.list", or in OCI parlance, an index.
It has an array field called `manifests`, each element of which lists one platform and the hash of the manifest for that platform.
The "platform" is a combination of "architecture" and "os". Since we will be running on the common
linux on amd64, we look for an entry in `manifests` that has a `platform` entry as follows:

```json
"platform": {
  "architecture": "amd64",
  "os": "linux"
}
```

This is the first one in the list, and it has the hash of `sha256:9bb13890319dc01e5f8a4d3d0c4c72685654d682d568350fd38a02b1d70aee6b`.

We then retrieve the item with that hash, specifically `docker.io/library/redis@sha256:9bb13890319dc01e5f8a4d3d0c4c72685654d682d568350fd38a02b1d70aee6b`
This gives us the manifest for the image on linux/amd64:

```json
{
    "schemaVersion": 2,
    "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
    "config": {
        "mediaType": "application/vnd.docker.container.image.v1+json",
        "size": 7648,
        "digest": "sha256:987b553c835f01f46eb1859bc32f564119d5833801a27b25a0ca5c6b8b6e111a"
    },
    "layers": [
        {
            "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
            "size": 27092228,
            "digest": "sha256:bb79b6b2107fea8e8a47133a660b78e3a546998fcf0427be39ac9a0af4a97e90"
        },
        {
            "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
            "size": 1732,
            "digest": "sha256:1ed3521a5dcbd05214eb7f35b952ecf018d5a6610c32ba4e315028c556f45e94"
        },
        {
            "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
            "size": 1417672,
            "digest": "sha256:5999b99cee8f2875d391d64df20b6296b63f23951a7d41749f028375e887cd05"
        },
        {
            "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
            "size": 7348264,
            "digest": "sha256:bfee6cb5fdad6b60ec46297f44542ee9d8ac8f01c072313a51cd7822df3b576f"
        },
        {
            "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
            "size": 98,
            "digest": "sha256:fd36a1ebc6728807cbb1aa7ef24a1861343c6dc174657721c496613c7b53bd07"
        },
        {
            "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
            "size": 409,
            "digest": "sha256:97481c7992ebf6f22636f87e4d7b79e962f928cdbe6f2337670fa6c9a9636f04"
        }
    ]
}
```

The `mediaType` tell us that this is a "manifest", and it fits the correct format:

* one `config`, whose hash is `sha256:987b553c835f01f46eb1859bc32f564119d5833801a27b25a0ca5c6b8b6e111a`
* one or more `layers`; in this example, there are 6 layers

Each of these elements - the index, the manifests, the config file and each of the layers - is stored
separately in the registry, and is downloaded independently.

### Content Store

When content is loaded into containerd's content store, it stores them very similarly to how the registry does.
Each component is stored in a file whose name is the hash of it.

Continuing our redis example, if we do `client.Pull()` or `ctr pull`, we will get the following in our
content store:

* `sha256:2a9865e55c37293b71df051922022898d8e4ec0f579c9b53a0caee1b170bc81c` - the index
* `sha256:9bb13890319dc01e5f8a4d3d0c4c72685654d682d568350fd38a02b1d70aee6b` - the manifest for `linux/amd64`
* `sha256:987b553c835f01f46eb1859bc32f564119d5833801a27b25a0ca5c6b8b6e111a` - the config
* `sha256:97481c7992ebf6f22636f87e4d7b79e962f928cdbe6f2337670fa6c9a9636f04` - layer 0
* `sha256:5999b99cee8f2875d391d64df20b6296b63f23951a7d41749f028375e887cd05` - layer 1
* `sha256:bfee6cb5fdad6b60ec46297f44542ee9d8ac8f01c072313a51cd7822df3b576f` - layer 2
* `sha256:fd36a1ebc6728807cbb1aa7ef24a1861343c6dc174657721c496613c7b53bd07` - layer 3
* `sha256:bb79b6b2107fea8e8a47133a660b78e3a546998fcf0427be39ac9a0af4a97e90` - layer 4
* `sha256:1ed3521a5dcbd05214eb7f35b952ecf018d5a6610c32ba4e315028c556f45e94` - layer 5

If we look in our content store, we see exactly these (I filtered and sorted to make it easier to read):

```console
$ tree /var/lib/containerd/io.containerd.content.v1.content/blobs
/var/lib/containerd/io.containerd.content.v1.content/blobs
└── sha256
    ├── 2a9865e55c37293b71df051922022898d8e4ec0f579c9b53a0caee1b170bc81c
    ├── 9bb13890319dc01e5f8a4d3d0c4c72685654d682d568350fd38a02b1d70aee6b
    ├── 987b553c835f01f46eb1859bc32f564119d5833801a27b25a0ca5c6b8b6e111a
    ├── 97481c7992ebf6f22636f87e4d7b79e962f928cdbe6f2337670fa6c9a9636f04
    ├── 5999b99cee8f2875d391d64df20b6296b63f23951a7d41749f028375e887cd05
    ├── bfee6cb5fdad6b60ec46297f44542ee9d8ac8f01c072313a51cd7822df3b576f
    ├── fd36a1ebc6728807cbb1aa7ef24a1861343c6dc174657721c496613c7b53bd07
    ├── bb79b6b2107fea8e8a47133a660b78e3a546998fcf0427be39ac9a0af4a97e90
    └── 1ed3521a5dcbd05214eb7f35b952ecf018d5a6610c32ba4e315028c556f45e94
```

We can see the same thing if we use the containerd interface. Again, we sorted it for consistent easier viewing.

```console
$ ctr content ls
DIGEST                                                                  SIZE    AGE             LABELS
sha256:2a9865e55c37293b71df051922022898d8e4ec0f579c9b53a0caee1b170bc81c 1.862kB 20 minutes      containerd.io/distribution.source.docker.io=library/redis,containerd.io/gc.ref.content.m.0=sha256:9bb13890319dc01e5f8a4d3d0c4c72685654d682d568350fd38a02b1d70aee6b,containerd.io/gc.ref.content.m.1=sha256:aeb53f8db8c94d2cd63ca860d635af4307967aa11a2fdead98ae0ab3a329f470,containerd.io/gc.ref.content.m.2=sha256:17dc42e40d4af0a9e84c738313109f3a95e598081beef6c18a05abb57337aa5d,containerd.io/gc.ref.content.m.3=sha256:613f4797d2b6653634291a990f3e32378c7cfe3cdd439567b26ca340b8946013,containerd.io/gc.ref.content.m.4=sha256:ee0e1f8d8d338c9506b0e487ce6c2c41f931d1e130acd60dc7794c3a246eb59e,containerd.io/gc.ref.content.m.5=sha256:1072145f8eea186dcedb6b377b9969d121a00e65ae6c20e9cd631483178ea7ed,containerd.io/gc.ref.content.m.6=sha256:4b7860fcaea5b9bbd6249c10a3dc02a5b9fb339e8aef17a542d6126a6af84d96,containerd.io/gc.ref.content.m.7=sha256:d66dfc869b619cd6da5b5ae9d7b1cbab44c134b31d458de07f7d580a84b63f69
sha256:9bb13890319dc01e5f8a4d3d0c4c72685654d682d568350fd38a02b1d70aee6b 1.572kB 20 minutes      containerd.io/distribution.source.docker.io=library/redis,containerd.io/gc.ref.content.config=sha256:987b553c835f01f46eb1859bc32f564119d5833801a27b25a0ca5c6b8b6e111a,containerd.io/gc.ref.content.l.0=sha256:bb79b6b2107fea8e8a47133a660b78e3a546998fcf0427be39ac9a0af4a97e90,containerd.io/gc.ref.content.l.1=sha256:1ed3521a5dcbd05214eb7f35b952ecf018d5a6610c32ba4e315028c556f45e94,containerd.io/gc.ref.content.l.2=sha256:5999b99cee8f2875d391d64df20b6296b63f23951a7d41749f028375e887cd05,containerd.io/gc.ref.content.l.3=sha256:bfee6cb5fdad6b60ec46297f44542ee9d8ac8f01c072313a51cd7822df3b576f,containerd.io/gc.ref.content.l.4=sha256:fd36a1ebc6728807cbb1aa7ef24a1861343c6dc174657721c496613c7b53bd07,containerd.io/gc.ref.content.l.5=sha256:97481c7992ebf6f22636f87e4d7b79e962f928cdbe6f2337670fa6c9a9636f04
sha256:987b553c835f01f46eb1859bc32f564119d5833801a27b25a0ca5c6b8b6e111a 7.648kB 20 minutes      containerd.io/distribution.source.docker.io=library/redis,containerd.io/gc.ref.snapshot.overlayfs=sha256:33bd296ab7f37bdacff0cb4a5eb671bcb3a141887553ec4157b1e64d6641c1cd
sha256:97481c7992ebf6f22636f87e4d7b79e962f928cdbe6f2337670fa6c9a9636f04 409B    20 minutes      containerd.io/distribution.source.docker.io=library/redis,containerd.io/uncompressed=sha256:d442ae63d423b4b1922875c14c3fa4e801c66c689b69bfd853758fde996feffb
sha256:5999b99cee8f2875d391d64df20b6296b63f23951a7d41749f028375e887cd05 1.418MB 20 minutes      containerd.io/distribution.source.docker.io=library/redis,containerd.io/uncompressed=sha256:223b15010c47044b6bab9611c7a322e8da7660a8268949e18edde9c6e3ea3700
sha256:bfee6cb5fdad6b60ec46297f44542ee9d8ac8f01c072313a51cd7822df3b576f 7.348MB 20 minutes      containerd.io/distribution.source.docker.io=library/redis,containerd.io/uncompressed=sha256:b96fedf8ee00e59bf69cf5bc8ed19e92e66ee8cf83f0174e33127402b650331d
sha256:fd36a1ebc6728807cbb1aa7ef24a1861343c6dc174657721c496613c7b53bd07 98B     20 minutes      containerd.io/distribution.source.docker.io=library/redis,containerd.io/uncompressed=sha256:aff00695be0cebb8a114f8c5187fd6dd3d806273004797a00ad934ec9cd98212
sha256:bb79b6b2107fea8e8a47133a660b78e3a546998fcf0427be39ac9a0af4a97e90 27.09MB 19 minutes      containerd.io/distribution.source.docker.io=library/redis,containerd.io/uncompressed=sha256:d0fe97fa8b8cefdffcef1d62b65aba51a6c87b6679628a2b50fc6a7a579f764c
sha256:1ed3521a5dcbd05214eb7f35b952ecf018d5a6610c32ba4e315028c556f45e94 1.732kB 20 minutes      containerd.io/distribution.source.docker.io=library/redis,containerd.io/uncompressed=sha256:832f21763c8e6b070314e619ebb9ba62f815580da6d0eaec8a1b080bd01575f7
```

#### Labels

Note that each blob of content has several labels on it. This sub-section describes the labels.
This is not intended to be a comprehensive overview of labels.

#### Common Labels
For images pulled from remotes, the `containerd.io.distribution.source.<registry>=[<repo/1>,<repo/2>]` label
is added to each blob of the image to indicate its source.
```
containerd.io/distribution.source.docker.io=library/redis
```

If the blob is shared by different repos in the same registry, the repo name will be appended:
```
containerd.io/distribution.source.docker.io=library/redis,myrepo/redis
```

##### Layer Labels

We start with the layers themselves. These have only one label: `containerd.io/uncompressed`. These files are
gzipped tar files; the value of the label gives the hash of them when uncompressed. You can get the same value
by doing:

```console
$ cat <file> | gunzip - | sha256sum -
```

For example:

```console
$ cat /var/lib/containerd/io.containerd.content.v1.content/blobs/sha256/1ed3521a5dcbd05214eb7f35b952ecf018d5a6610c32ba4e315028c556f45e94 | gunzip - | sha256sum -
832f21763c8e6b070314e619ebb9ba62f815580da6d0eaec8a1b080bd01575f7
```

That aligns precisely with the last layer:

```
sha256:1ed3521a5dcbd05214eb7f35b952ecf018d5a6610c32ba4e315028c556f45e94 1.732kB 20 minutes      containerd.io/distribution.source.docker.io=library/redis,containerd.io/uncompressed=sha256:832f21763c8e6b070314e619ebb9ba62f815580da6d0eaec8a1b080bd01575f7
```

##### Config Labels

We have a single config layer, `sha256:987b553c835f01f46eb1859bc32f564119d5833801a27b25a0ca5c6b8b6e111a`. It has a label prefixed with `containerd.io/gc.ref.` indicating
that it is a label that impacts garbage collection.

In this case, the label is `containerd.io/gc.ref.snapshot.overlayfs` and has a value of `sha256:33bd296ab7f37bdacff0cb4a5eb671bcb3a141887553ec4157b1e64d6641c1cd`.

This is used to connect this config to a snapshot. We will look at that shortly when we discuss snapshots.

##### Manifest Labels

The labels on the manifest also begin with `containerd.io/gc.ref`, indicating that they are used to control
garbage collection. A manifest has several "children". These normally are the config and the layers. We want
to ensure that as long as the image remains around, i.e. the manifest, the children do not get garbage collected.
Thus, we have labels referencing each child:
* `containerd.io/gc.ref.content.config` references the config
* `containerd.io/gc.ref.content.l.<index>` reference the layers

In our example, the manifest is `sha256:9bb13890319dc01e5f8a4d3d0c4c72685654d682d568350fd38a02b1d70aee6b`, and the labels are as follows.

```
containerd.io/gc.ref.content.config=sha256:df57482065789980ee9445b1dd79ab1b7b3d1dc26b6867d94470af969a64c8e6
containerd.io/gc.ref.content.l.0=sha256:97481c7992ebf6f22636f87e4d7b79e962f928cdbe6f2337670fa6c9a9636f04
containerd.io/gc.ref.content.l.1=sha256:5999b99cee8f2875d391d64df20b6296b63f23951a7d41749f028375e887cd05
containerd.io/gc.ref.content.l.2=sha256:bfee6cb5fdad6b60ec46297f44542ee9d8ac8f01c072313a51cd7822df3b576f
containerd.io/gc.ref.content.l.3=sha256:fd36a1ebc6728807cbb1aa7ef24a1861343c6dc174657721c496613c7b53bd07
containerd.io/gc.ref.content.l.4=sha256:bb79b6b2107fea8e8a47133a660b78e3a546998fcf0427be39ac9a0af4a97e90
containerd.io/gc.ref.content.l.5=sha256:1ed3521a5dcbd05214eb7f35b952ecf018d5a6610c32ba4e315028c556f45e94
```

These are precisely those children of the manifest - the config and layers - that are stored in our content store.

##### Index Labels

The labels on the index also begin with `containerd.io/gc.ref`, indicating that they are used to control
garbage collection. An index has several "children", i.e. the manifests, one for each platform, as discussed above.
We want to ensure that as long as the index remains around, the children do not get garbage collected.
Thus, we have labels referencing each child, `containerd.io/gc.ref.content.m.<index>`.

In our example, the index is `sha256:2a9865e55c37293b71df051922022898d8e4ec0f579c9b53a0caee1b170bc81c`, and the labels are as follows:

```
containerd.io/gc.ref.content.m.0=sha256:9bb13890319dc01e5f8a4d3d0c4c72685654d682d568350fd38a02b1d70aee6b
containerd.io/gc.ref.content.m.1=sha256:aeb53f8db8c94d2cd63ca860d635af4307967aa11a2fdead98ae0ab3a329f470
containerd.io/gc.ref.content.m.2=sha256:17dc42e40d4af0a9e84c738313109f3a95e598081beef6c18a05abb57337aa5d
containerd.io/gc.ref.content.m.3=sha256:613f4797d2b6653634291a990f3e32378c7cfe3cdd439567b26ca340b8946013
containerd.io/gc.ref.content.m.4=sha256:ee0e1f8d8d338c9506b0e487ce6c2c41f931d1e130acd60dc7794c3a246eb59e
containerd.io/gc.ref.content.m.5=sha256:1072145f8eea186dcedb6b377b9969d121a00e65ae6c20e9cd631483178ea7ed
containerd.io/gc.ref.content.m.6=sha256:4b7860fcaea5b9bbd6249c10a3dc02a5b9fb339e8aef17a542d6126a6af84d96
containerd.io/gc.ref.content.m.7=sha256:d66dfc869b619cd6da5b5ae9d7b1cbab44c134b31d458de07f7d580a84b63f69
```

Notice that there are 8 children to the index, but all of them are for platforms other than ours, `linux/amd64`,
and thus only one of them, `sha256:9bb13890319dc01e5f8a4d3d0c4c72685654d682d568350fd38a02b1d70aee6b` actually is
in our content store. That doesn't hurt; it just means that the others will not be garbage collected either. Since
they aren't there, they won't be removed.

### Snapshots

The content in the content store is unusable directly by containers.

First, it is immutable, which makes it difficult for containers to use as a container filesystem.
Second, the format itself often is unusable directly. For example,
most container layers are in a tar-gzip format, with each tar-gzip file representing a single layer to be applied on top of the previous layers.
One cannot simply mount a tar-gzip file. Even if one could, one would need to apply the changes from each layer on top of the previous.
Third, some content layer media-types, like the standard container layer, include not only normal file additions and modifications,
but removals. None of this can be used directly by a container, which requires a normal filesystem mount.

In order to use the content for an image, we create snapshots of the content.

The process is as follows:

1. The snapshotter creates a snapshot from the parent. In the case of the first layer, that is blank. This is now an "active" snapshot.
1. The diff applier, which has knowledge of the internal format of the layer blob, applies the layer blob to the active snapshot.
1. The snapshotter commits the snapshot after the diff has been applied. This is now a "committed" snapshot.
1. The committed snapshot is used as the parent for the next layer.

containerd ships with several built-in snapshotters, the default of which is `overlayfs`. You can choose a different snapshotter for each unpack
of an image and creating a container. See [snapshotters](./snapshotters/README.md) and [PLUGINS](./PLUGINS.md).

Returning to our example, each layer will have a corresponding immutable snapshot layer. Recalling that
our example has 6 layers, we expect to see 6 committed snapshots. The output has been sorted to make viewing
easier; it matches the layers from the content store and manifest itself.

```console
$ ctr snapshot ls
KEY                                                                     PARENT                                                                  KIND
sha256:33bd296ab7f37bdacff0cb4a5eb671bcb3a141887553ec4157b1e64d6641c1cd sha256:bc8b010e53c5f20023bd549d082c74ef8bfc237dc9bbccea2e0552e52bc5fcb1 Committed
sha256:bc8b010e53c5f20023bd549d082c74ef8bfc237dc9bbccea2e0552e52bc5fcb1 sha256:aa4b58e6ece416031ce00869c5bf4b11da800a397e250de47ae398aea2782294 Committed
sha256:aa4b58e6ece416031ce00869c5bf4b11da800a397e250de47ae398aea2782294 sha256:a8f09c4919857128b1466cc26381de0f9d39a94171534f63859a662d50c396ca Committed
sha256:a8f09c4919857128b1466cc26381de0f9d39a94171534f63859a662d50c396ca sha256:2ae5fa95c0fce5ef33fbb87a7e2f49f2a56064566a37a83b97d3f668c10b43d6 Committed
sha256:2ae5fa95c0fce5ef33fbb87a7e2f49f2a56064566a37a83b97d3f668c10b43d6 sha256:d0fe97fa8b8cefdffcef1d62b65aba51a6c87b6679628a2b50fc6a7a579f764c Committed
sha256:d0fe97fa8b8cefdffcef1d62b65aba51a6c87b6679628a2b50fc6a7a579f764c                                                                         Committed
```

If we look in the snapshot directory, which is specific to each snapshotter, we see the snapshots themselves.

```bash
# cd /var/lib/containerd
# ls io.containerd.snapshotter.v1.overlayfs/snapshots/
1  2  3  4  5  6
```

There are 6 snapshots, each corresponding to one listed from `ctr snapshot ls`, above. The directories themselves contain the actual content:

```bash
# ls io.containerd.snapshotter.v1.overlayfs/snapshots/1/fs
bin  boot  dev  etc  home  lib  lib64  media  mnt  opt  proc  root  run  sbin  srv  sys  tmp  usr  var
# ls io.containerd.snapshotter.v1.overlayfs/snapshots/2/fs
etc  var
```

These are the unpacked and applied contents of the first and second layers.

#### Parents

Each snapshot has a parent, except for the root. It is a tree, or a stacked cake, starting with the first layer.
This matches how the layers are built, as layers.

#### Name

The key, or name, for the snapshot does not match the hash from the content store. This is because the hash from the
content store is the hash of the _original_ content, in this case tar-gzipped. The snapshot expands it out into the
filesystem to make it useful. It also does not match the uncompressed content, i.e. the tar file without gzip, and as
given on the label `containerd.io/uncompressed`.

Rather the name is the result of applying the layer to the previous one and hashing it. By that logic, the very root
of the tree, the first layer, should have the same hash and name as the uncompressed value of the first layer blob.
Indeed, it does. The root layer is `sha256:bb79b6b2107fea8e8a47133a660b78e3a546998fcf0427be39ac9a0af4a97e90 `
which, when uncompressed, has the value `sha256:d0fe97fa8b8cefdffcef1d62b65aba51a6c87b6679628a2b50fc6a7a579f764c`,
which is the first layer in the snapshot, and also the label on that layer in the content store:

```
sha256:bb79b6b2107fea8e8a47133a660b78e3a546998fcf0427be39ac9a0af4a97e90 27.09MB 19 minutes      containerd.io/distribution.source.docker.io=library/redis,containerd.io/uncompressed=sha256:d0fe97fa8b8cefdffcef1d62b65aba51a6c87b6679628a2b50fc6a7a579f764c
```

#### Final Layer

The final, or top, layer, is the point at which you would want to create an active snapshot to start a container.
Thus, we would need to track it. This is exactly the label that is placed on the config. In our example, the
config is at `sha256:987b553c835f01f46eb1859bc32f564119d5833801a27b25a0ca5c6b8b6e111a` and had the label
`containerd.io/gc.ref.snapshot.overlayfs=sha256:33bd296ab7f37bdacff0cb4a5eb671bcb3a141887553ec4157b1e64d6641c1cd`.

Looking at our snapshots, the value of the final layer of the stack is, indeed, that:

```
sha256:33bd296ab7f37bdacff0cb4a5eb671bcb3a141887553ec4157b1e64d6641c1cd sha256:bc8b010e53c5f20023bd549d082c74ef8bfc237dc9bbccea2e0552e52bc5fcb1 Committed
```

Note as well, that the label on the config in the content store starts with `containerd.io/gc.ref`. This is
a garbage collection label. It is this label that keeps the garbage collector from removing the snapshot.
Because the config has a reference to it, the top layer is "protected" from garbage collection. This layer,
in turn, depends on the next layer down, so it is protected from collection, and so on until the root or base layer.

### Container

With the above in place, we know how to create an active snapshot that is useful for the container. We simply
need to [Prepare()](https://godoc.org/github.com/containerd/containerd/v2/snapshots#Snapshotter) the active snapshot,
passing it an ID and the parent, in this case the top layer of committed snapshots.

We can see this by creating two containers from the same image. Both will create active snapshots on top of the top committed
snapshot. However, we expect to see only 2 new snapshots, each active. The committed snapshots are unchanged, as they are reused.

```console
# ctr container create docker.io/library/redis:5.0.6 redis1
# ctr container create docker.io/library/redis:5.0.6 redis2
ctr snapshot ls
KEY                                                                     PARENT                                                                  KIND
redis1                                                                  sha256:33bd296ab7f37bdacff0cb4a5eb671bcb3a141887553ec4157b1e64d6641c1cd Active
redis2                                                                  sha256:33bd296ab7f37bdacff0cb4a5eb671bcb3a141887553ec4157b1e64d6641c1cd Active
sha256:33bd296ab7f37bdacff0cb4a5eb671bcb3a141887553ec4157b1e64d6641c1cd sha256:bc8b010e53c5f20023bd549d082c74ef8bfc237dc9bbccea2e0552e52bc5fcb1 Committed
sha256:bc8b010e53c5f20023bd549d082c74ef8bfc237dc9bbccea2e0552e52bc5fcb1 sha256:aa4b58e6ece416031ce00869c5bf4b11da800a397e250de47ae398aea2782294 Committed
sha256:aa4b58e6ece416031ce00869c5bf4b11da800a397e250de47ae398aea2782294 sha256:a8f09c4919857128b1466cc26381de0f9d39a94171534f63859a662d50c396ca Committed
sha256:a8f09c4919857128b1466cc26381de0f9d39a94171534f63859a662d50c396ca sha256:2ae5fa95c0fce5ef33fbb87a7e2f49f2a56064566a37a83b97d3f668c10b43d6 Committed
sha256:2ae5fa95c0fce5ef33fbb87a7e2f49f2a56064566a37a83b97d3f668c10b43d6 sha256:d0fe97fa8b8cefdffcef1d62b65aba51a6c87b6679628a2b50fc6a7a579f764c Committed
sha256:d0fe97fa8b8cefdffcef1d62b65aba51a6c87b6679628a2b50fc6a7a579f764c                                                                         Committed
```

The same 6 committed layers exist, but only 2 new active snapshots are created, one for each container. Both have the parent of the top committed snapshot,
`sha256:33bd296ab7f37bdacff0cb4a5eb671bcb3a141887553ec4157b1e64d6641c1cd`.

Thus, the steps are:

1. Get the content into the content store, either via [Pull()](https://godoc.org/github.com/containerd/containerd/v2/client#Client.Pull), or via loading it in the [content.Store API](https://godoc.org/github.com/containerd/containerd/v2/content#Store)
1. Unpack the image to create committed snapshots for each layer, using [image.Unpack()](https://godoc.org/github.com/containerd/containerd/v2/client#Image). Alternatively, if you use [Pull()](https://godoc.org/github.com/containerd/containerd/v2/client#Client.Pull), you can pass it an option to unpack when pulling, using [WithPullUnpack()](https://godoc.org/github.com/containerd/containerd/v2/client#WithPullUnpack).
1. Create an active snapshot using [Prepare()](https://godoc.org/github.com/containerd/containerd/v2/snapshots#Snapshotter). You can skip this step if you plan on creating a container, as you can pass it as an option to the next step.
1. Create a container using [NewContainer()](https://godoc.org/github.com/containerd/containerd/v2/client#Client.NewContainer), optionally telling it to create a snapshot with [WithNewSnapshot()](https://godoc.org/github.com/containerd/containerd/v2/client#WithNewSnapshot)
