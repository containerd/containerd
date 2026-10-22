# EROFS Layer Types for OCI Images

## 1. Introduction & Motivation

This document proposes a specification for distributing EROFS (Enhanced Read-Only File System) layers within OCI (Open Container Initiative) images.

EROFS is a read-only filesystem designed for high performance and storage efficiency. Enabling native EROFS layers in container images offers several key advantages:

*   **Random Access at Runtime (Lazy Loading):** Containers can start immediately without waiting for the entire image to download or unpack. Data is fetched on-demand.
*   **End-to-End Integrity:** Using DM-Verity, the integrity of the filesystem can be verified at the block level by the kernel during runtime, providing stronger security guarantees than file-level checksums.
*   **Efficient Distribution:** Distributing the filesystem image directly avoids local conversion steps (e.g., unpacking tarballs and creating filesystems), reducing startup latency and CPU usage.

## 2. Layer Formats

This specification defines two primary layer formats: **Uncompressed** and **Compressed (Zstd)**. Both formats support optional DM-Verity data for runtime integrity.

### 2.1 Uncompressed EROFS Layer

This layer consists of a standard EROFS filesystem image. It may optionally append DM-Verity hash tree data at the end of the blob.

**Structure:**

```text
+-----------------------------------------+
|                                         |
|  EROFS Filesystem Image                 |
|  (Standard EROFS superblock & data)     |
|                                         |
+-----------------------------------------+
|                                         |
|  DM-Verity Hash Data (Optional)         |
|  (Appended at the end)                  |
|                                         |
+-----------------------------------------+
```

### 2.2 Compressed EROFS Layer (Zstd)

This layer uses Zstd compression to reduce transfer size. Unlike standard compressed layers (like `.tar.gzip`), this format supports random access by utilizing a **Chunk Mapping Table**.

The blob consists of:
1.  **Zstd Compressed Frames:** The raw EROFS filesystem image data compressed in standard [Zstd frames](https://github.com/facebook/zstd/blob/dev/doc/zstd_compression_format.md#frames).
2.  **Chunk Mapping Table:** A custom table stored inside a [Zstd Skippable Frame](https://github.com/facebook/zstd/blob/dev/doc/zstd_compression_format.md#skippable-frames). This allows standard Zstd decompressors to ignore it, while aware readers can use it to locate specific uncompressed chunks.
3.  **DM-Verity Hash Data (Optional):** Appended at the end.

**Structure:**

```text
+-----------------------------------------+ <--- Start of Blob
|                                         |
|  Zstd Compressed Frames                 |
|  (Compressed Raw EROFS Image)           |
|                                         |
+-----------------------------------------+ <--- Offset via Annotation
|                                         |
|  Skippable Zstd Frame (0x184D2A5E)      |
|  +-----------------------------------+  |
|  | Chunk Mapping Table               |  |
|  +-----------------------------------+  |
|                                         |
+-----------------------------------------+ <--- Offset via Annotation
|                                         |
|  DM-Verity Hash Data (Optional)         |
|                                         |
+-----------------------------------------+ <--- End of Blob
```

## 3. Binary Format Specification

All multi-byte integers are stored in **Little-Endian** format unless otherwise specified.

### 3.1 Chunk Mapping Table

The Chunk Mapping Table allows mapping uncompressed offsets to compressed ranges within the Zstd stream. It is stored as the payload of a Zstd Skippable Frame.

**Header:**

| Field | Size (Bytes) | Description |
| :--- | :--- | :--- |
| Magic | 4 | Magic number (`0xCD 0xE4 0xEC 0x67`) |
| Version | 4 | Format version (currently `1`) |
| Uncompressed Size | 8 | Total size of the uncompressed data |
| Chunk Size | 4 | Size of each uncompressed chunk (e.g., 4mib) |
| Hash Algo | 1 | Algorithm for chunk checksums (0=None, 1=SHA-256) |
| Hash Size | 1 | Size of the hash in bytes (e.g., 32 for SHA-256) |
| Reserved | 2 | Reserved for future use (must be 0) |

**Chunk Entry:**

There is one entry for every chunk. The index of the entry corresponds to the chunk index (Uncompressed Offset / Chunk Size).

| Field | Size (Bytes) | Description |
| :--- | :--- | :--- |
| Block Offset | 8 | Absolute offset in the blob where this compressed chunk begins |
| Checksum | N | (Optional) Checksum of the *compressed* block data. Size `N` defined in Header. |

*Note: The size of the compressed block is calculated by: `NextEntry.Offset - CurrentEntry.Offset`.*

### 3.2 DM-Verity Data

If present, the DM-Verity data is a raw dump of the Merkle tree used by the Linux kernel `dm-verity` target. Its location is defined by an OCI annotation.

## 4. OCI Integration

### 4.1 Media Types

*   **`application/vnd.erofs.layer.v1`**
    *   Uncompressed EROFS filesystem.
    *   May include inline EROFS compression (handled internally by EROFS), but the layer blob itself is not compressed for distribution.
    *   Optional DM-Verity data appended.

*   **`application/vnd.erofs.layer.v1+zstd`**
    *   Zstd compressed EROFS filesystem.
    *   MUST contain the Chunk Mapping Table in a skippable frame for random access.
    *   Optional DM-Verity data appended.

### 4.2 Annotations

Metadata required for random access and verification is passed via OCI Manifest annotations.

| Annotation Key | Description |
| :--- | :--- |
| `dev.containerd.erofs.zstd.chunk_table_offset` | **Required for Zstd:** Byte offset to the start of the Zstd Skippable Frame containing the Chunk Mapping Table. |
| `dev.containerd.erofs.zstd.chunk_digest` | **Required for Zstd:** Digest of the chunk map formatted as an OCI digest. |
| `dev.containerd.erofs.dmverity.root_digest` | **Required for Verity:** The root hash of the DM-Verity tree formatted as an OCI digest. |
| `dev.containerd.erofs.dmverity.offset` | **Required for Random Access with Verity:** Byte offset where the DM-Verity data begins. If not present, it can be recalculated and must match the root digest if provided. |
| `dev.containerd.erofs.dmverity.block_size` | **Optional:** Block size used for DM-Verity (default: 4096). |

*Future Goal: Transition `dev.containerd.*` annotations to a standardized namespace (e.g. `org.opencontainers.*`) upon wider adoption.*


### 4.3 Layer DiffID

The Layer DiffID is used by the OCI image config to uniquely identify the
uncompressed content of a layer. It is included in the rootfs section of the
config. It is important that the DiffID represents a secure hash of the content,
ensuring that the digest of an image config is an immutable representation of a
runnable image. For EROFS, either the digest of the uncompressed EROFS filesystem
image or the root hash of the DM-Verity tree can be used as the DiffID. If the
DM-Verity data is present, it must be used as the DiffID.