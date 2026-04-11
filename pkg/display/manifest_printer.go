/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package display

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/erofs/go-erofs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/term"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/internal/erofsutils/seekable"
)

// TreeFormat is used to format tree based output using 4 values.
// Each value must display with the same total width to format correctly.
//
// MiddleDrop is used to show a child element which is not the last child
// LastDrop is used to show the last child element
// SkipLine is used for displaying data from a previous child before the next child
// Spacer is used to display child data for the last child
type TreeFormat struct {
	MiddleDrop string
	LastDrop   string
	SkipLine   string
	Spacer     string
}

// LineTreeFormat uses line drawing characters to format a tree
//
// TreeRoot
// ├── First child       # MiddleDrop =  "├── "
// │   Skipped line      # SkipLine = "│   "
// └── Last child        # LastDrop = "└── "
// ....└── Only child    # Spacer="....", LastDrop = "└── "
var LineTreeFormat = TreeFormat{
	MiddleDrop: "├── ",
	LastDrop:   "└── ",
	SkipLine:   "│   ",
	Spacer:     "    ",
}

type ImageTreePrinter struct {
	verbose bool
	w       io.Writer
	format  TreeFormat
}

type PrintOpt func(*ImageTreePrinter)

func Verbose(p *ImageTreePrinter) {
	p.verbose = true
}

func WithWriter(w io.Writer) PrintOpt {
	return func(p *ImageTreePrinter) {
		p.w = w
	}
}

func WithFormat(format TreeFormat) PrintOpt {
	return func(p *ImageTreePrinter) {
		p.format = format
	}
}

func NewImageTreePrinter(opts ...PrintOpt) *ImageTreePrinter {
	p := &ImageTreePrinter{
		verbose: false,
		w:       os.Stdout,
		format:  LineTreeFormat,
	}
	for _, opt := range opts {
		opt(p)
	}

	return p
}

// PrintImageTree prints an image and all its sub elements
func (p *ImageTreePrinter) PrintImageTree(ctx context.Context, img images.Image, store content.InfoReaderProvider) error {
	fmt.Fprintln(p.w, img.Name)
	subchild := p.format.SkipLine
	fmt.Fprintf(p.w, "%s Created: %s\n", subchild, img.CreatedAt)
	fmt.Fprintf(p.w, "%s Updated: %s\n", subchild, img.UpdatedAt)
	for k, v := range img.Labels {
		fmt.Fprintf(p.w, "%s Label %q: %q\n", subchild, k, v)
	}
	return p.printManifestTree(ctx, img.Target, store, p.format.LastDrop, p.format.Spacer)
}

// PrintManifestTree prints a manifest and all its sub elements
func (p *ImageTreePrinter) PrintManifestTree(ctx context.Context, desc ocispec.Descriptor, store content.InfoReaderProvider) error {
	// start displaying tree from the root descriptor perspective, which is a single child view
	return p.printManifestTree(ctx, desc, store, p.format.LastDrop, p.format.Spacer)
}

func (p *ImageTreePrinter) printManifestTree(ctx context.Context, desc ocispec.Descriptor, store content.InfoReaderProvider, prefix, childprefix string) error {
	subprefix := childprefix + p.format.MiddleDrop
	subchild := childprefix + p.format.SkipLine
	fmt.Fprintf(p.w, "%s%s @%s (%d bytes)\n", prefix, desc.MediaType, desc.Digest, desc.Size)

	if desc.Platform != nil && desc.Platform.Architecture != "" {
		fmt.Fprintf(p.w, "%s Platform: %s\n", subchild, platforms.Format(*desc.Platform))
	}
	b, err := content.ReadBlob(ctx, store, desc)
	if err != nil {
		if errdefs.IsNotFound(err) {
			// If the blob is not found, we can still display the tree
			fmt.Fprintf(p.w, "%s Content does not exist locally, skipping\n", childprefix+p.format.LastDrop)
			return nil
		}
		return err
	}
	if err := p.showContent(ctx, store, desc, subchild); err != nil {
		return err
	}

	if images.IsManifestType(desc.MediaType) {
		var manifest ocispec.Manifest
		if err := json.Unmarshal(b, &manifest); err != nil {
			return err
		}

		if len(manifest.Layers) == 0 {
			subprefix = childprefix + p.format.LastDrop
			subchild = childprefix + p.format.Spacer
		}
		fmt.Fprintf(p.w, "%s%s @%s (%d bytes)\n", subprefix, manifest.Config.MediaType, manifest.Config.Digest, manifest.Config.Size)

		if err := p.showContent(ctx, store, manifest.Config, subchild); err != nil {
			return err
		}

		for i := range manifest.Layers {
			if len(manifest.Layers) == i+1 {
				subprefix = childprefix + p.format.LastDrop
				subchild = childprefix + p.format.Spacer
			}
			fmt.Fprintf(p.w, "%s%s @%s (%d bytes)\n", subprefix, manifest.Layers[i].MediaType, manifest.Layers[i].Digest, manifest.Layers[i].Size)

			if err := p.showContent(ctx, store, manifest.Layers[i], subchild); err != nil {
				return err
			}
		}
	} else if images.IsIndexType(desc.MediaType) {
		var idx ocispec.Index
		if err := json.Unmarshal(b, &idx); err != nil {
			return err
		}

		for i := range idx.Manifests {
			if len(idx.Manifests) == i+1 {
				subprefix = childprefix + p.format.LastDrop
				subchild = childprefix + p.format.Spacer
			}
			if err := p.printManifestTree(ctx, idx.Manifests[i], store, subprefix, subchild); err != nil {
				return err
			}
		}
	}

	return nil
}

func (p *ImageTreePrinter) showContent(ctx context.Context, store content.InfoReaderProvider, desc ocispec.Descriptor, prefix string) error {
	if p.verbose {
		info, err := store.Info(ctx, desc.Digest)
		if err != nil {
			return err
		}
		if len(info.Labels) > 0 {
			fmt.Fprintf(p.w, "%s┌────────Labels─────────\n", prefix)
			for k, v := range info.Labels {
				fmt.Fprintf(p.w, "%s│%q: %q\n", prefix, k, v)
			}
			fmt.Fprintf(p.w, "%s└───────────────────────\n", prefix)
		}
		if strings.HasSuffix(desc.MediaType, "json") {
			// Print content for config
			cb, err := content.ReadBlob(ctx, store, desc)
			if err != nil {
				return err
			}
			dst := bytes.NewBuffer(nil)
			if err := json.Indent(dst, cb, prefix+"│", "   "); err != nil {
				return fmt.Errorf("invalid JSON content for %s: %w", desc.Digest, err)
			}
			fmt.Fprintf(p.w, "%s┌────────Content────────\n", prefix)
			fmt.Fprintf(p.w, "%s│%s\n", prefix, strings.TrimSpace(dst.String()))
			fmt.Fprintf(p.w, "%s└───────────────────────\n", prefix)
		} else if images.IsSeekableErofsMediaType(desc.MediaType) {
			if err := p.showSeekableErofs(ctx, store, desc, prefix); err != nil {
				return err
			}
		} else if strings.HasPrefix(desc.MediaType, images.MediaTypeErofsLayer) {
			fmt.Fprintf(p.w, "%s┌──────EROFS Layer──────\n", prefix)

			f, err := store.ReaderAt(ctx, desc)
			if err != nil {
				return err
			}
			defer f.Close()
			img, err := erofs.Open(f)
			if err != nil {
				return err
			}

			fmt.Fprintf(p.w, "%s│ /\n", prefix)
			PrintDirectory(p.w, img, "/", prefix+"│ ", maxDirectoryDepth)

			fmt.Fprintf(p.w, "%s└───────────────────────\n", prefix)

		}
	}
	return nil
}

const (
	maxChunkPreview    = 3         // chunk entries to show before truncating
	maxDecompressBytes = 256 << 20 // skip directory tree if EROFS > 256 MiB
	maxDirectoryDepth  = 3         // directory tree recursion limit
)

func (p *ImageTreePrinter) showSeekableErofs(ctx context.Context, store content.InfoReaderProvider, desc ocispec.Descriptor, prefix string) error {
	fmt.Fprintf(p.w, "%s┌──Seekable EROFS Layer─\n", prefix)

	ann := desc.Annotations

	// Display annotations.
	if v, ok := ann[seekable.AnnotationChunkTableOffset]; ok {
		fmt.Fprintf(p.w, "%s│ Chunk table offset: %s\n", prefix, v)
	}
	if v, ok := ann[seekable.AnnotationChunkDigest]; ok {
		fmt.Fprintf(p.w, "%s│ Chunk digest:       %s\n", prefix, v)
	}
	if v, ok := ann[seekable.AnnotationDMVerityOffset]; ok {
		fmt.Fprintf(p.w, "%s│ DM-verity offset:   %s\n", prefix, v)
	}
	if v, ok := ann[seekable.AnnotationDMVerityRootDigest]; ok {
		fmt.Fprintf(p.w, "%s│ DM-verity root:     %s\n", prefix, v)
	}

	blob, err := store.ReaderAt(ctx, desc)
	if err != nil {
		fmt.Fprintf(p.w, "%s│ (unable to read blob: %v)\n", prefix, err)
		fmt.Fprintf(p.w, "%s└───────────────────────\n", prefix)
		return nil
	}
	defer blob.Close()

	// Parse and display chunk table (required for valid seekable EROFS).
	chunkTableOffsetStr := ann[seekable.AnnotationChunkTableOffset]
	if chunkTableOffsetStr == "" {
		fmt.Fprintf(p.w, "%s│ (missing chunk table offset annotation)\n", prefix)
		fmt.Fprintf(p.w, "%s└───────────────────────\n", prefix)
		return fmt.Errorf("seekable EROFS layer %s missing required annotation %s", desc.Digest, seekable.AnnotationChunkTableOffset)
	}
	chunkTableOffset, err := strconv.ParseInt(chunkTableOffsetStr, 10, 64)
	if err != nil {
		fmt.Fprintf(p.w, "%s└───────────────────────\n", prefix)
		return fmt.Errorf("invalid chunk table offset %q for %s: %w", chunkTableOffsetStr, desc.Digest, err)
	}
	chunkDigest := ann[seekable.AnnotationChunkDigest]
	tbl, err := seekable.ReadChunkTable(ctx, blob, chunkTableOffset, chunkDigest)
	if err != nil {
		fmt.Fprintf(p.w, "%s└───────────────────────\n", prefix)
		return fmt.Errorf("failed to read chunk table for %s: %w", desc.Digest, err)
	}
	p.printChunkTable(tbl, chunkTableOffset, prefix)

	fmt.Fprintf(p.w, "%s│\n", prefix)

	// Decompress and show EROFS directory tree, unless the image is too large.
	if tbl.Header.UncompressedSizeBytes > maxDecompressBytes {
		fmt.Fprintf(p.w, "%s│ (EROFS image is %.1f MiB, too large to display directory tree)\n",
			prefix, float64(tbl.Header.UncompressedSizeBytes)/(1024*1024))
	} else {
		blobReader := io.NewSectionReader(blob, 0, blob.Size())
		var decompBuf bytes.Buffer
		if _, decErr := seekable.DecodeErofsAll(ctx, blobReader, &decompBuf); decErr != nil {
			fmt.Fprintf(p.w, "%s│ (decompression error: %v)\n", prefix, decErr)
		} else {
			erofsImg, erofsErr := erofs.Open(bytes.NewReader(decompBuf.Bytes()))
			if erofsErr != nil {
				fmt.Fprintf(p.w, "%s│ (EROFS parse error: %v)\n", prefix, erofsErr)
			} else {
				fmt.Fprintf(p.w, "%s│ /\n", prefix)
				PrintDirectory(p.w, erofsImg, "/", prefix+"│ ", maxDirectoryDepth)
			}
		}
	}

	fmt.Fprintf(p.w, "%s└───────────────────────\n", prefix)
	return nil
}

func (p *ImageTreePrinter) printChunkTable(tbl *seekable.ChunkTable, chunkTableOffset int64, prefix string) {
	hdr := tbl.Header

	var hashName string
	switch hdr.HashAlgo {
	case seekable.ChunkHashAlgoNone:
		hashName = "none"
	case seekable.ChunkHashAlgoSHA512:
		hashName = "SHA-512"
	default:
		hashName = fmt.Sprintf("unknown(%d)", hdr.HashAlgo)
	}

	fmt.Fprintf(p.w, "%s│\n", prefix)
	fmt.Fprintf(p.w, "%s│ Chunk Table:\n", prefix)
	fmt.Fprintf(p.w, "%s│   Uncompressed size: %d bytes (%.2f MiB)\n", prefix,
		hdr.UncompressedSizeBytes, float64(hdr.UncompressedSizeBytes)/(1024*1024))
	fmt.Fprintf(p.w, "%s│   Chunk size:        %d bytes (%.2f MiB)\n", prefix,
		hdr.ChunkSizeBytes, float64(hdr.ChunkSizeBytes)/(1024*1024))
	fmt.Fprintf(p.w, "%s│   Hash algorithm:    %s\n", prefix, hashName)
	fmt.Fprintf(p.w, "%s│   Chunks:            %d\n", prefix, len(tbl.Entries))

	n := len(tbl.Entries)
	if n <= maxChunkPreview+2 {
		for i := range tbl.Entries {
			p.printChunkEntry(tbl, i, chunkTableOffset, hashName, prefix)
		}
	} else {
		for i := range maxChunkPreview {
			p.printChunkEntry(tbl, i, chunkTableOffset, hashName, prefix)
		}
		fmt.Fprintf(p.w, "%s│   ... (%d more chunks)\n", prefix, n-maxChunkPreview-1)
		p.printChunkEntry(tbl, n-1, chunkTableOffset, hashName, prefix)
	}
}

func (p *ImageTreePrinter) printChunkEntry(tbl *seekable.ChunkTable, i int, chunkTableOffset int64, hashName, prefix string) {
	entry := tbl.Entries[i]
	var frameEnd int64
	if i+1 < len(tbl.Entries) {
		frameEnd = tbl.Entries[i+1].BlockOffset
	} else {
		frameEnd = chunkTableOffset
	}
	compSize := frameEnd - entry.BlockOffset

	checksumStr := ""
	if len(entry.Checksum) > 0 {
		full := hex.EncodeToString(entry.Checksum)
		if len(full) > 16 {
			checksumStr = full[:16] + "..."
		} else {
			checksumStr = full
		}
	}

	fmt.Fprintf(p.w, "%s│   chunk[%d]: offset=%-10d compressed=%-10d bytes", prefix, i, entry.BlockOffset, compSize)
	if checksumStr != "" {
		fmt.Fprintf(p.w, "  %s=%s", hashName, checksumStr)
	}
	fmt.Fprintln(p.w)
}

func PrintDirectory(f io.Writer, fsys fs.FS, dir string, prefix string, maxDepth int) {
	dirEnts, err := fs.ReadDir(fsys, dir)
	if err != nil {
		fmt.Fprintf(f, "%sError reading directory %q: %v\n", prefix, dir, err)
	}
	var files, dirs []string
	for _, entry := range dirEnts {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		} else {
			f := entry.Name()
			if strings.Contains(f, " ") {
				f = fmt.Sprintf("%q", f)
			}
			files = append(files, f)
		}

	}
	if len(files) > 0 {
		spacer := "  "
		if len(dirs) > 0 {
			spacer = "│ "
		}
		width := terminalWidth(f)
		if width > len(prefix)+len(spacer)+10 {
			var b strings.Builder
			b.WriteString(prefix)
			b.WriteString(spacer)
			for i, file := range files {
				if b.Len()+len(file) > width && i > 0 {
					fmt.Fprintln(f, strings.TrimRight(b.String(), " "))
					b.Reset()
					b.WriteString(prefix)
					b.WriteString(spacer)
				}
				b.WriteString(file)
				b.WriteString(" ")
			}
			fmt.Fprintln(f, strings.TrimRight(b.String(), " "))
		} else {
			for _, file := range files {
				fmt.Fprintf(f, "%s%s%s\n", prefix, spacer, file)
			}
		}
	}
	if maxDepth <= 0 {
		if len(dirs) > 0 {
			fmt.Fprintf(f, "%s... (%d subdirectories)\n", prefix, len(dirs))
		}
		return
	}
	for i, d := range dirs {
		isLast := i == len(dirs)-1
		var newPrefix string
		if isLast {
			fmt.Fprintf(f, "%s└─ %s/\n", prefix, d)
			newPrefix = prefix + "    "
		} else {
			fmt.Fprintf(f, "%s├─ %s/\n", prefix, d)
			newPrefix = prefix + "│  "
		}
		PrintDirectory(f, fsys, path.Join(dir, d), newPrefix, maxDepth-1)
	}

}

func terminalWidth(w io.Writer) int {
	// Check if w has FD method
	type fdWriter interface {
		Fd() uintptr
	}
	fw, ok := w.(fdWriter)
	if !ok {
		return -1
	}
	// Get the file descriptor
	fd := int(fw.Fd())

	// Check if the file descriptor is a terminal before attempting to get its size
	if !term.IsTerminal(fd) {
		return -1
	}

	// Get the terminal width and height
	width, _, err := term.GetSize(fd)
	if err != nil {
		return -1
	}

	return width
}
