## `internal/erofsutils/seekable`

This package implements the core logic for the seekable EROFS layer format, as defined in https://github.com/containerd/containerd/pull/12703


The purpose of this format is to:
- use zstd compression to compress the EROFS blob for transport efficiency
- supports random access, to support a future lazy loading snapshotter
- support e2e verification with dm-verity

At a high-level, each layer is constructed with three data zones:
1. the EROFS data
  1. this is constructed by chunking the EROFS image at fixed sized offsets, and compressing each chunk as an independent zstd frame
1. a skippable zstd frame containing a chunk-mapping table
1. an optional skippable zstd frame containing dm-verity data

```text
[normal zstd frames: chunked EROFS image] [skippable: chunk table] [skippable: dm-verity data (optional)]
```


### Use-cases

- Create a seekable erofs image:
  - via ctr: `ctr images convert --erofs-seekable` (see `--erofs-chunk-size`, `--erofs-dm-verity`)
  - provides sensible defaults
  - converts each layer to erofs
  - generates the zstd stream of the erofs layer
  - generates the chunk mapping table
  - optionally generates dm-verity data
  - constructs an image from those layers, with media-type `application/vnd.erofs.layer.v1+zstd`

- Unpack/differ (for the eager erofs snapshotter that exists today):
  - reads a blob from the content store
  - reads the entire zstd erofs region, and decompresses it
  - writes a mountable `layer.erofs`
  - if dm-verity is present, reads the embedded dm-verity payload bytes and writes `DMVerityMetadata` for mount-time
  - *note* the eager snapshotter ignores the chunk-mapping table

- dm-verity data available at mount-time (for the eager erofs snapshotter that exists today):
  - reads `DMVerityMetadata` in addition to `layer.erofs` mount information
    - assumes `DMVerityMetadata` is the JSON file written alongside `layer.erofs` during unpack as per [this PR](https://github.com/containerd/containerd/pull/12502/files)
  - opens/activates a dm-verity device
  - mounts the verified device path as the erofs layer

- Support a future lazy-loading remote snapshotter:
  - reads in the chunk-mapping table
  - uses the chunk-mapping table to load in only the relevant zstd chunks
  - exact API & control flow tbd


### Detailed View

```text
File offset →
0                                                                                                   EOF
┌──────────────────────────────────────────────────────────────────────────────────────────────────────┐
│           EROFS DATA (concatenated zstd frames; one frame per uncompressed chunk)                    │
│                                                                                                      │
│   Chunk 0 frame bytes                                                                                │
│   ┌──────────────────────────────────────────────────────────────────────────────┐                   │
│   │ Normal Zstd Frame (independent)                                              │                   │
│   │   starts with magic 0xFD2FB528                                               │                   │
│   └──────────────────────────────────────────────────────────────────────────────┘                   │
│   Chunk 1 frame bytes                                                                                │
│   ┌──────────────────────────────────────────────────────────────────────────────┐                   │
│   │ Normal Zstd Frame (independent)                                              │                   │
│   │   starts with magic 0xFD2FB528                                               │                   │
│   └──────────────────────────────────────────────────────────────────────────────┘                   │
│   ...                                                                                                │
│   Chunk N frame bytes                                                                                │
│   ┌──────────────────────────────────────────────────────────────────────────────┐                   │
│   │ Normal Zstd Frame (independent)                                              │                   │
│   │   starts with magic 0xFD2FB528                                               │                   │
│   └──────────────────────────────────────────────────────────────────────────────┘                   │
│                                                                                                      │
│   endOfChunks = chunk_table_offset                                                                   │
├──────────────────────────────────────────────────────────────────────────────────────────────────────┤
│           CHUNK MAPPING TABLE (ONE zstd *skippable* frame; always present)                           │
│                                                                                                      │
│ chunk_table_offset  → ┌──────────────────────────────────────────────────────────────────────────┐   │
│                       │ Skippable Frame (ID=0, magic=0x184D2A50)                                 │   │
│                       │   [8-byte skippable header] + [payload bytes = Chunk Mapping Table]      │   │
│                       └──────────────────────────────────────────────────────────────────────────┘   │
│                                                                                                      │
│   Note: chunk_table_offset points at the skippable header for the chunk table.                       │
├──────────────────────────────────────────────────────────────────────────────────────────────────────┤
│           DM-VERITY (ONE zstd *skippable* frame; present only if dm-verity is enabled)               │
│                                                                                                      │
│   dmverity.offset → ┌────────────────────────────────────────────────────────────────────────────┐   │
│                     │ Skippable Frame (ID=0, magic=0x184D2A50)                                   │   │
│                     │   [8-byte skippable header] + [payload bytes = dm-verity data]             │   │
│                     └────────────────────────────────────────────────────────────────────────────┘   │
│                                                                                                      │
│   Note: dmverity.offset points at the skippable frame header for dm-verity.                           │
│                                                                                                      │
│  EOF immediately after the dm-verity skippable frame (when present); otherwise EOF after chunk table.│
└──────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

## Super-detailed view

This table shows the full data layout expected in the layer:

```text

+===================+==========+===========================+============+==============================================+
| Structure         | Kind     | Field                     | Size(bytes)| Notes                                        |
+===================+==========+===========================+============+==============================================+
|       Erofs image chunked and compressed across k zstd frames-- each frame consists of:                              |
| Normal zstd frame | Metadata | Frame Magic               | 4          | 0xFD2FB528 (bytes: 28 B5 2F FD)              |
| Normal zstd frame | Metadata | Frame Header Descriptor   | 1          | bitfield declaring optional header fields    |
| Normal zstd frame | Metadata | Window Descriptor         | 0 or 1     | present for non-single-segment frames        |
| Normal zstd frame | Metadata | Dictionary ID             | 0/1/2/4    | optional; size per descriptor                |
| Normal zstd frame | Metadata | Frame Content Size        | 0/1/2/4/8  | optional; size per descriptor                |
|   Data blocks                                                                                                        |
| Normal zstd frame | Metadata | Block Header              | 3 (each)   | repeated; last-block flag + type + size      |
| Normal zstd frame | Data     | Block Payload             | variable   | repeated; format depends on block type       |
|   [more blocks]                                                                                                      |
| Normal zstd frame | Metadata | zstd Content Checksum     | 0 or 4     | optional trailer (zstd-defined)              |
| [more frames]                                                                                                        |
+===================+==========+===========================+============+==============================================+
|                        Chunk-mapping table                                                                           |
| Skippable frame   | Metadata | Skippable Magic           | 4          | default: 0x184D2A50                          |
| Skippable frame   | Metadata | Payload Size              | 4          | payload byte length N                        |
|   Payload                                                                                                            |
| Table header      | Metadata | Magic                     | 4          | 0xCDE4EC67                                   |
| Table header      | Metadata | Version                   | 4          | 1                                            |
| Table header      | Metadata | Uncompressed Size         | 8          | total uncompressed bytes                     |
| Table header      | Metadata | Chunk Size                | 4          | uncompressed bytes per chunk                 |
| Table header      | Metadata | Hash Algo                 | 1          | 0=None, 1=SHA-512                            |
| Table header      | Metadata | Reserved                  | 2          | must be 0                                    |
| Chunk entry       | Data     | Block Offset              | 8          | absolute file offset of chunk zstd frame     |
| Chunk entry       | Data     | Checksum                  | N          | optional; N derived from Hash Algo           |
| [more entries]    |          |                           |            |                                              |
+===================+==========+===========================+============+==============================================+
|                        Optional dm-verity data: may be absent                                                        |
| Skippable frame   | Metadata | Skippable Magic           | 4          | 0x184D2A50..0x184D2A5F (default: 0x184D2A50) |
| Skippable frame   | Metadata | Payload Size              | 4          | payload byte length N                        |
|   Payload                                                                                                            |
| dm-verity payload | Data     | Superblock                | 512        | contains ASCII "verity" signature            |
| dm-verity payload | Data     | Superblock Padding        | 3584       | zero-filled to reach 4096 bytes              |
| dm-verity payload | Data     | Merkle Tree               | variable   | computed over (padded) uncompressed EROFS    |
+===================+==========+===========================+============+==============================================+

Note: When dm-verity is enabled, the superblock is always present (required).
```
