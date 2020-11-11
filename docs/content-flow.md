# Content Flow

A major goal of containerd is to create a system wherein content can be used for executing containers.
In order to execute on that flow, containerd requires content and to manage it.

This document describes how content flows into containerd, how it is managed, and where it exists
at each stage in the process. We use an example of going from a known image
[docker.io/library/redis:5.0.9](https://hub.docker.com/layers/redis/library/redis/5.0.9/images/sha256-4ff8940144391ecd5e1632d0c427d95f4a8d2bb4a72b7e3898733352350d9ab3?context=explore) to explore the
flow of content.

## Content Areas

Content exists in several areas in the containerd lifecycle:

* OCI registry, for example [hub.docker.com](https://hub.docker.com) or [quay.io](https://quay.io)
* containerd content store, under containerd's local storage space, for example, on a standard Linux installation at `/var/lib/containerd/io.containerd.content.v1.content`
* snapshots, under containerd's local storage space, for example, on a standard Linux installation at `/var/lib/containerd/io.containerd.snapshotter.v1.<type>`. For an overlayfs snapshotter, that would be at `/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs`

In order to create a container, the following must occur:

1. The image and all its content must be loaded into the content store. This normally happens via download from the OCI registry, but you can load content in directly as well.
1. Committed snapshots must be created from each layer of content for the image.
1. An active snapshot must be created on top of the final layer of content for the image.

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
      "digest": "sha256:a5aae2581826d13e906ff5c961d4c2817a9b96c334fd97b072d976990384156a",
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
      "platform": {
        "architecture": "amd64",
        "os": "linux"
      },
      "size": 1572
    },
    {
      "digest": "sha256:4ff8940144391ecd5e1632d0c427d95f4a8d2bb4a72b7e3898733352350d9ab3",
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
      "platform": {
        "architecture": "arm",
        "os": "linux",
        "variant": "v5"
      },
      "size": 1573
    },
    {
      "digest": "sha256:ce541c3e2570b5a05d40e7fc01f87fc1222a701c81f95e7e6f2ef6df1c6e25e7",
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
      "platform": {
        "architecture": "arm",
        "os": "linux",
        "variant": "v7"
      },
      "size": 1573
    },
    {
      "digest": "sha256:535ee258100feeeb525d4793c16c7e58147c105231d7d05ffc9c84b56750f233",
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
      "platform": {
        "architecture": "arm64",
        "os": "linux",
        "variant": "v8"
      },
      "size": 1573
    },
    {
      "digest": "sha256:0f3b047f2789547c58634ce88d71c7856999b2afc8b859b7adb5657043984b26",
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
      "platform": {
        "architecture": "386",
        "os": "linux"
      },
      "size": 1572
    },
    {
      "digest": "sha256:bfc45f499a9393aef091057f3d067ff7129ae9fb30d9f31054bafe96ca30b8d6",
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
      "platform": {
        "architecture": "mips64le",
        "os": "linux"
      },
      "size": 1572
    },
    {
      "digest": "sha256:3198e1f1707d977939154a57918d360a172c575bddeac875cb26ca6f4d30dc1c",
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
      "platform": {
        "architecture": "ppc64le",
        "os": "linux"
      },
      "size": 1573
    },
    {
      "digest": "sha256:24a15cc9366e1557db079a987e63b98a5abf4dee4356a096442f53ddc8b9c7e9",
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

This is the first one in the list, and it has the hash of `sha256:a5aae2581826d13e906ff5c961d4c2817a9b96c334fd97b072d976990384156a`.

We then retrieve the item with that hash, specifically `docker.io/library/redis@sha256:a5aae2581826d13e906ff5c961d4c2817a9b96c334fd97b072d976990384156a`
This gives us the manifest for the image on linux/amd64:

```json
{
   "schemaVersion": 2,
   "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
   "config": {
      "mediaType": "application/vnd.docker.container.image.v1+json",
      "size": 6836,
      "digest": "sha256:df57482065789980ee9445b1dd79ab1b7b3d1dc26b6867d94470af969a64c8e6"
   },
   "layers": [
      {
         "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
         "size": 27098147,
         "digest": "sha256:123275d6e508d282237a22fefa5aef822b719a06496444ea89efa65da523fc4b"
      },
      {
         "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
         "size": 1730,
         "digest": "sha256:f2edbd6a658e04d559c1bec36d838006bbdcb39d8fb9033ed43d2014ac497774"
      },
      {
         "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
         "size": 1417708,
         "digest": "sha256:66960bede47c1a193710cf8bfa7bf5f50bc46374260923df1db1c423b52153ac"
      },
      {
         "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
         "size": 7345094,
         "digest": "sha256:79dc0b596c9027416a627a6237bd080ac9d87f92b60f1ce145c566632839bce7"
      },
      {
         "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
         "size": 99,
         "digest": "sha256:de36df38e0b6c0e7f29913c68884a0323207c07cd7c1eba71d5618f525ac2ba6"
      },
      {
         "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
         "size": 410,
         "digest": "sha256:602cd484ff92015489f7b9cf9cbd77ac392997374b1cc42937773f5bac1ff43b"
      }
   ]
}
```

The `mediaType` tell us that this is a "manifest", and it fits the correct format:

* one `config`, whose hash is `sha256:df57482065789980ee9445b1dd79ab1b7b3d1dc26b6867d94470af969a64c8e6`
* one or more `layers`; in this example, there are 6 layers

Each of these elements - the index, the manifests, the config file and each of the layers - is stored
separately in the registry, and is downloaded independently.

### Content Store

When content is loaded into containerd's content store, it stores them very similarly to how the registry does.
Each component is stored in a file whose name is the hash of it.

Continuing our redis example, if we do `client.Pull()` or `ctr pull`, we will get the following in our
content store:

* `sha256:1d0b903e3770c2c3c79961b73a53e963f4fd4b2674c2c4911472e8a054cb5728` - the index
* `sha256:a5aae2581826d13e906ff5c961d4c2817a9b96c334fd97b072d976990384156a` - the manifest for `linux/amd64`
* `sha256:df57482065789980ee9445b1dd79ab1b7b3d1dc26b6867d94470af969a64c8e6` - the config
* `sha256:123275d6e508d282237a22fefa5aef822b719a06496444ea89efa65da523fc4b` - layer 0
* `sha256:f2edbd6a658e04d559c1bec36d838006bbdcb39d8fb9033ed43d2014ac497774` - layer 1
* `sha256:66960bede47c1a193710cf8bfa7bf5f50bc46374260923df1db1c423b52153ac` - layer 2
* `sha256:79dc0b596c9027416a627a6237bd080ac9d87f92b60f1ce145c566632839bce7` - layer 3
* `sha256:de36df38e0b6c0e7f29913c68884a0323207c07cd7c1eba71d5618f525ac2ba6` - layer 4
* `sha256:602cd484ff92015489f7b9cf9cbd77ac392997374b1cc42937773f5bac1ff43b` - layer 5

If we look in our content store, we see exactly these (I filtered and sorted to make it easier to read):

```console
$ tree /var/lib/containerd/io.containerd.content.v1.content/blobs
/var/lib/containerd/io.containerd.content.v1.content/blobs
└── sha256
    ├── 1d0b903e3770c2c3c79961b73a53e963f4fd4b2674c2c4911472e8a054cb5728
    ├── a5aae2581826d13e906ff5c961d4c2817a9b96c334fd97b072d976990384156a
    ├── df57482065789980ee9445b1dd79ab1b7b3d1dc26b6867d94470af969a64c8e6
    ├── 123275d6e508d282237a22fefa5aef822b719a06496444ea89efa65da523fc4b
    ├── f2edbd6a658e04d559c1bec36d838006bbdcb39d8fb9033ed43d2014ac497774
    ├── 66960bede47c1a193710cf8bfa7bf5f50bc46374260923df1db1c423b52153ac
    ├── 79dc0b596c9027416a627a6237bd080ac9d87f92b60f1ce145c566632839bce7
    ├── de36df38e0b6c0e7f29913c68884a0323207c07cd7c1eba71d5618f525ac2ba6
    └── 602cd484ff92015489f7b9cf9cbd77ac392997374b1cc42937773f5bac1ff43b
```

We can see the same thing if we use the containerd interface. Again, we sorted it for consistent easier viewing.

```console
ctr content ls
DIGEST									SIZE		AGE		LABELS
sha256:1d0b903e3770c2c3c79961b73a53e963f4fd4b2674c2c4911472e8a054cb5728	1.862 kB	6 minutes	containerd.io/gc.ref.content.0=sha256:a5aae2581826d13e906ff5c961d4c2817a9b96c334fd97b072d976990384156a,containerd.io/gc.ref.content.1=sha256:4ff8940144391ecd5e1632d0c427d95f4a8d2bb4a72b7e3898733352350d9ab3,containerd.io/gc.ref.content.2=sha256:ce541c3e2570b5a05d40e7fc01f87fc1222a701c81f95e7e6f2ef6df1c6e25e7,containerd.io/gc.ref.content.3=sha256:535ee258100feeeb525d4793c16c7e58147c105231d7d05ffc9c84b56750f233,containerd.io/gc.ref.content.4=sha256:0f3b047f2789547c58634ce88d71c7856999b2afc8b859b7adb5657043984b26,containerd.io/gc.ref.content.5=sha256:bfc45f499a9393aef091057f3d067ff7129ae9fb30d9f31054bafe96ca30b8d6,containerd.io/gc.ref.content.6=sha256:3198e1f1707d977939154a57918d360a172c575bddeac875cb26ca6f4d30dc1c,containerd.io/gc.ref.content.7=sha256:24a15cc9366e1557db079a987e63b98a5abf4dee4356a096442f53ddc8b9c7e9
sha256:a5aae2581826d13e906ff5c961d4c2817a9b96c334fd97b072d976990384156a	1.572 kB	6 minutes	containerd.io/gc.ref.content.2=sha256:f2edbd6a658e04d559c1bec36d838006bbdcb39d8fb9033ed43d2014ac497774,containerd.io/gc.ref.content.3=sha256:66960bede47c1a193710cf8bfa7bf5f50bc46374260923df1db1c423b52153ac,containerd.io/gc.ref.content.4=sha256:79dc0b596c9027416a627a6237bd080ac9d87f92b60f1ce145c566632839bce7,containerd.io/gc.ref.content.5=sha256:de36df38e0b6c0e7f29913c68884a0323207c07cd7c1eba71d5618f525ac2ba6,containerd.io/gc.ref.content.6=sha256:602cd484ff92015489f7b9cf9cbd77ac392997374b1cc42937773f5bac1ff43b,containerd.io/gc.ref.content.0=sha256:df57482065789980ee9445b1dd79ab1b7b3d1dc26b6867d94470af969a64c8e6,containerd.io/gc.ref.content.1=sha256:123275d6e508d282237a22fefa5aef822b719a06496444ea89efa65da523fc4b
sha256:df57482065789980ee9445b1dd79ab1b7b3d1dc26b6867d94470af969a64c8e6	6.836 kB	6 minutes	containerd.io/gc.ref.snapshot.overlayfs=sha256:87806a591ce894ff5c699c28fe02093d6cdadd6b1ad86819acea05ccb212ff3d
sha256:123275d6e508d282237a22fefa5aef822b719a06496444ea89efa65da523fc4b	27.1 MB		6 minutes	containerd.io/uncompressed=sha256:b60e5c3bcef2f42ec42648b3acf7baf6de1fa780ca16d9180f3b4a3f266fe7bc
sha256:f2edbd6a658e04d559c1bec36d838006bbdcb39d8fb9033ed43d2014ac497774	1.73 kB		6 minutes	containerd.io/uncompressed=sha256:b5a8df342567aa93d568b263b25c1eaf52655f0952e1911742ffb4f7a521e044
sha256:66960bede47c1a193710cf8bfa7bf5f50bc46374260923df1db1c423b52153ac	1.418 MB	6 minutes	containerd.io/uncompressed=sha256:c03c7e9701eb61f1e2232f6d19faa699cd9d346207aaf4f50d84b1e37bbad3e2
sha256:79dc0b596c9027416a627a6237bd080ac9d87f92b60f1ce145c566632839bce7	7.345 MB	6 minutes	containerd.io/uncompressed=sha256:367024e4e00618a9ada3203b5922d3186a0aa6136a1c4cbf5ed380171e1afe48
sha256:de36df38e0b6c0e7f29913c68884a0323207c07cd7c1eba71d5618f525ac2ba6	99 B		6 minutes	containerd.io/uncompressed=sha256:60ef3ee42de712ef7748cc8e92192e926180b1be6fec9580933f1347fb6b2747
sha256:602cd484ff92015489f7b9cf9cbd77ac392997374b1cc42937773f5bac1ff43b	410 B		6 minutes	containerd.io/uncompressed=sha256:bab68e5155b7010010964bf3aadc30e4a9c625701314ff6fa3c143c72f0aeb9c
```

#### Labels

Note that each chunk of content has several labels on it. This sub-section describes the labels.
This is not intended to be a comprehensive overview of labels.

##### Layer Labels

We start with the layers themselves. These have only one label: `containerd.io/uncompressed`. These files are
gzipped tar files; the value of the label gives the hash of them when uncompressed. You can get the same value
by doing:

```console
$ cat <file> | gunzip - | sha256sum -
```

For example:

```console
$ cat /var/lib/containerd/io.containerd.content.v1.content/blobs/sha256/602cd484ff92015489f7b9cf9cbd77ac392997374b1cc42937773f5bac1ff43b | gunzip - | sha256sum -
bab68e5155b7010010964bf3aadc30e4a9c625701314ff6fa3c143c72f0aeb9c
```

That aligns precisely with the last layer:

```
sha256:602cd484ff92015489f7b9cf9cbd77ac392997374b1cc42937773f5bac1ff43b	410 B		6 minutes	containerd.io/uncompressed=sha256:bab68e5155b7010010964bf3aadc30e4a9c625701314ff6fa3c143c72f0aeb9c
```

##### Config Labels

We have a single config layer, `sha256:df57482065789980ee9445b1dd79ab1b7b3d1dc26b6867d94470af969a64c8e6`. It has a label prefixed with `containerd.io/gc.ref.` indicating
that it is a label that impacts garbage collection.

In this case, the label is `containerd.io/gc.ref.snapshot.overlayfs` and has a value of `sha256:87806a591ce894ff5c699c28fe02093d6cdadd6b1ad86819acea05ccb212ff3d`.

This is used to connect this config to a snapshot. We will look at that shortly when we discuss snapshots.

##### Manifest Labels

The labels on the manifest also begin with `containerd.io/gc.ref`, indicating that they are used to control
garbage collection. A manifest has several "children". These normally are the config and the layers. We want
to ensure that as long as the image remains around, i.e. the manifest, the children do not get garbage collected.
Thus, we have labels referencing each child, `containerd.io/gc.ref.content.<index>`.

In our example, the manifest is `sha256:a5aae2581826d13e906ff5c961d4c2817a9b96c334fd97b072d976990384156a`, and the labels are as follows.

```
containerd.io/gc.ref.content.0=sha256:df57482065789980ee9445b1dd79ab1b7b3d1dc26b6867d94470af969a64c8e6
containerd.io/gc.ref.content.1=sha256:123275d6e508d282237a22fefa5aef822b719a06496444ea89efa65da523fc4b
containerd.io/gc.ref.content.2=sha256:f2edbd6a658e04d559c1bec36d838006bbdcb39d8fb9033ed43d2014ac497774
containerd.io/gc.ref.content.3=sha256:66960bede47c1a193710cf8bfa7bf5f50bc46374260923df1db1c423b52153ac
containerd.io/gc.ref.content.4=sha256:79dc0b596c9027416a627a6237bd080ac9d87f92b60f1ce145c566632839bce7
containerd.io/gc.ref.content.5=sha256:de36df38e0b6c0e7f29913c68884a0323207c07cd7c1eba71d5618f525ac2ba6
containerd.io/gc.ref.content.6=sha256:602cd484ff92015489f7b9cf9cbd77ac392997374b1cc42937773f5bac1ff43b
```

These are precisely those children of the manifest - the config and layers - that are stored in our content store.

##### Index Labels

The labels on the index also begin with `containerd.io/gc.ref`, indicating that they are used to control
garbage collection. An index has several "children", i.e. the manifests, one for each platform, as discussed above.
We want to ensure that as long as the index remains around, the children do not get garbage collected.
Thus, we have labels referencing each child, `containerd.io/gc.ref.content.<index>`.

In our example, the index is `sha256:1d0b903e3770c2c3c79961b73a53e963f4fd4b2674c2c4911472e8a054cb5728`, and the labels are as follows:

```
containerd.io/gc.ref.content.0=sha256:a5aae2581826d13e906ff5c961d4c2817a9b96c334fd97b072d976990384156a
containerd.io/gc.ref.content.1=sha256:4ff8940144391ecd5e1632d0c427d95f4a8d2bb4a72b7e3898733352350d9ab3
containerd.io/gc.ref.content.2=sha256:ce541c3e2570b5a05d40e7fc01f87fc1222a701c81f95e7e6f2ef6df1c6e25e7
containerd.io/gc.ref.content.3=sha256:535ee258100feeeb525d4793c16c7e58147c105231d7d05ffc9c84b56750f233
containerd.io/gc.ref.content.4=sha256:0f3b047f2789547c58634ce88d71c7856999b2afc8b859b7adb5657043984b26
containerd.io/gc.ref.content.5=sha256:bfc45f499a9393aef091057f3d067ff7129ae9fb30d9f31054bafe96ca30b8d6
containerd.io/gc.ref.content.6=sha256:3198e1f1707d977939154a57918d360a172c575bddeac875cb26ca6f4d30dc1c
containerd.io/gc.ref.content.7=sha256:24a15cc9366e1557db079a987e63b98a5abf4dee4356a096442f53ddc8b9c7e9
```

Notice that there are 8 children to the index, but all of them are for platforms other than ours, `linux/amd64`,
and thus only one of them, `sha256:a5aae2581826d13e906ff5c961d4c2817a9b96c334fd97b072d976990384156a` actually is
in our content store. That doesn't hurt; it just means that the others will not be garbage collected either. Since
they aren't there, they won't be removed.

### Snapshots

The content in the content store is immutable, but also in formats that often are unusable. For example,
most container layers are in a tar-gzip format. One cannot simply mount a tar-gzip file. Even if one could,
we want to leave our immutable content not only unchanged, but unchangeable, even by accident, i.e. immutable.

In order to use it, we create snapshots of the content.

The process is as follows:

1. The snapshotter creates a snapshot from the parent. In the case of the first layer, that is blank. This is now an "active" snapshot.
1. The diff applier, which has knowledge of the internal format of the layer blob, applies the layer blob to the active snapshot.
1. The snapshotter commits the snapshot after the diff has been applied. This is now a "committed" snapshot.
1. The committed snapshot is used as the parent for the next layer.

Returning to our example, each layer will have a corresponding immutable snapshot layer. Recalling that
our example has 6 layers, we expect to see 6 committed snapshots. The output has been sorted to make viewing
easier; it matches the layers from the content store and manifest itself.

```
# ctr snapshot ls
KEY                                                                     PARENT                                                                  KIND
sha256:b60e5c3bcef2f42ec42648b3acf7baf6de1fa780ca16d9180f3b4a3f266fe7bc                                                                         Committed
sha256:c2cba74b5b43db78068241279a3225ca4f9639c17a5f0ce019489ee71b4382a5 sha256:b60e5c3bcef2f42ec42648b3acf7baf6de1fa780ca16d9180f3b4a3f266fe7bc Committed
sha256:315768cd0d297e3cb707360f8dde646419940b42e055845a160880cf98b5a242 sha256:c2cba74b5b43db78068241279a3225ca4f9639c17a5f0ce019489ee71b4382a5 Committed
sha256:13aa829f25ce405c1c5f40e0449b9270ce162ac7e4c2a81359df6fe09f939afd sha256:315768cd0d297e3cb707360f8dde646419940b42e055845a160880cf98b5a242 Committed
sha256:814ff1c8753c9cd3942089a2401f1806a1133f27b6875bcad7b7e68846e205e4 sha256:13aa829f25ce405c1c5f40e0449b9270ce162ac7e4c2a81359df6fe09f939afd Committed
sha256:87806a591ce894ff5c699c28fe02093d6cdadd6b1ad86819acea05ccb212ff3d sha256:814ff1c8753c9cd3942089a2401f1806a1133f27b6875bcad7b7e68846e205e4 Committed
```

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
Indeed, it does. The root layer is `sha256:123275d6e508d282237a22fefa5aef822b719a06496444ea89efa65da523fc4b`
which, when uncompressed, has the value `sha256:b60e5c3bcef2f42ec42648b3acf7baf6de1fa780ca16d9180f3b4a3f266fe7bc`,
which is the first layer in the snapshot, and also the label on that layer in the content store:

```
sha256:123275d6e508d282237a22fefa5aef822b719a06496444ea89efa65da523fc4b	27.1 MB		6 minutes	containerd.io/uncompressed=sha256:b60e5c3bcef2f42ec42648b3acf7baf6de1fa780ca16d9180f3b4a3f266fe7bc
```

#### Final Layer

The final, or top, layer, is the point at which you would want to create an active snapshot to start a container.
Thus, we would need to track it. This is exactly the label that is placed on the config. In our example, the
config is at `sha256:df57482065789980ee9445b1dd79ab1b7b3d1dc26b6867d94470af969a64c8e6` and had the label
`containerd.io/gc.ref.snapshot.overlayfs=sha256:87806a591ce894ff5c699c28fe02093d6cdadd6b1ad86819acea05ccb212ff3d`.

Looking at our snapshots, the value of the final layer of the stack is, indeed, that:

```
sha256:87806a591ce894ff5c699c28fe02093d6cdadd6b1ad86819acea05ccb212ff3d sha256:814ff1c8753c9cd3942089a2401f1806a1133f27b6875bcad7b7e68846e205e4 Committed
```

Note as well, that the label on the config in the content store starts with `containerd.io/gc.ref`. This is
a garbage collection label. It is this label that keeps the garbage collector from removing the snapshot.
Because the config has a reference to it, the top layer is "protected" from garbage collection. This layer,
in turn, depends on the next layer down, so it is protected from collection, and so on until the root or base layer.

### Container

With the above in place, we know how to create an active snapshot that is useful for the container. We simply
need to [Prepare()](https://godoc.org/github.com/containerd/containerd/snapshots#Snapshotter) the active snapshot,
passing it an ID and the parent, in this case the top layer of committed snapshots.

Thus, the steps are:

1. Get the content into the content store, either via [Pull()](https://godoc.org/github.com/containerd/containerd#Client.Pull), or via loading it in the [content.Store API](https://godoc.org/github.com/containerd/containerd/content#Store)
1. Unpack the image to create committed snapshots for each layer, using [image.Unpack()](https://godoc.org/github.com/containerd/containerd#Image). Alternatively, if you use [Pull()](https://godoc.org/github.com/containerd/containerd#Client.Pull), you can pass it an option to unpack when pulling, using [WithPullUnpack()](https://godoc.org/github.com/containerd/containerd#WithPullUnpack)
1. Create an active snapshot using [Prepare()](https://godoc.org/github.com/containerd/containerd/snapshots#Snapshotter). You can skip this step if you plan on creating a container, as you can pass it as an option to the next step.
1. Create a container using [NewContainer()](https://godoc.org/github.com/containerd/containerd#Client.NewContainer), optionally telling it to create a snapshot with [WithNewSnapshot()](https://godoc.org/github.com/containerd/containerd#WithNewSnapshot)
