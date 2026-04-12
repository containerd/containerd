//go:build linux

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

package seekable

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/internal/dmverity"
)

func TestDMVerityMetadata_RoundTrip(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	layerPath := filepath.Join(tmpDir, "layer.erofs")

	t.Log("create a dummy layer file")
	if err := os.WriteFile(layerPath, []byte("fake erofs data"), 0644); err != nil {
		t.Fatalf("create layer file: %v", err)
	}

	t.Log("create dm-verity metadata struct")
	original := dmverity.DmverityMetadata{
		RootHash:   "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		HashOffset: 4096,
	}

	t.Log("write metadata to JSON file")
	if err := dmverity.WriteMetadata(layerPath, &original); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	t.Log("verify the metadata file was created")
	metaPath := layerPath + ".dmverity"
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("metadata file not created: %v", err)
	}

	t.Log("read metadata back and compare fields")
	got, err := dmverity.ReadMetadata(layerPath)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}

	if got.RootHash != original.RootHash {
		t.Errorf("RootHash: got %q, want %q", got.RootHash, original.RootHash)
	}
	if got.HashOffset != original.HashOffset {
		t.Errorf("HashOffset: got %d, want %d", got.HashOffset, original.HashOffset)
	}
}

func buildSkippableFrame(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := writeSkippableFrameHeader(&buf, zstdSkippableMagicMin, uint32(len(payload))); err != nil {
		t.Fatalf("writeSkippableFrameHeader: %v", err)
	}
	if _, err := buf.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	return buf.Bytes()
}

func TestReadDMVerityPayload_ExtractsPayload(t *testing.T) {
	t.Parallel()

	payload := []byte("dm-verity-test-payload")
	blob := buildSkippableFrame(t, payload)

	section, size, err := readDMVerityPayload(bytes.NewReader(blob), int64(len(blob)), 0)
	if err != nil {
		t.Fatalf("readDMVerityPayload: %v", err)
	}
	if size != int64(len(payload)) {
		t.Fatalf("payload size: got %d, want %d", size, len(payload))
	}
	got, err := io.ReadAll(section)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch")
	}
}

func TestReadDMVerityPayload_OutOfBounds(t *testing.T) {
	t.Parallel()

	payload := []byte("dm-verity-test-payload")
	blob := buildSkippableFrame(t, payload)

	_, _, err := readDMVerityPayload(bytes.NewReader(blob), int64(len(blob)-1), 0)
	if err == nil {
		t.Fatalf("expected out-of-bounds error, got nil")
	}
}

func TestAppendDMVerityPayloadToLayer_PadsAndAppends(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	layerPath := filepath.Join(tmpDir, "layer.erofs")

	initial := []byte("abc") // len=3
	if err := os.WriteFile(layerPath, initial, 0644); err != nil {
		t.Fatalf("write initial layer: %v", err)
	}

	layerFile, err := os.OpenFile(layerPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open layer file: %v", err)
	}
	defer layerFile.Close()

	payload := []byte("verity-payload")
	blockSize := 4
	offset, bytesWritten, err := appendDMVerityPayloadToLayer(context.Background(), layerFile, bytes.NewReader(payload), int64(len(payload)), blockSize)
	if err != nil {
		t.Fatalf("appendDMVerityPayloadToLayer: %v", err)
	}
	if offset != 4 {
		t.Fatalf("dm-verity offset: got %d, want %d", offset, 4)
	}
	if bytesWritten != int64(1+len(payload)) {
		t.Fatalf("bytesWritten: got %d, want %d", bytesWritten, 1+len(payload))
	}

	got, err := os.ReadFile(layerPath)
	if err != nil {
		t.Fatalf("read layer file: %v", err)
	}
	expected := append(append([]byte{}, initial...), 0)
	expected = append(expected, payload...)
	if !bytes.Equal(got, expected) {
		t.Fatalf("layer contents mismatch")
	}
}
