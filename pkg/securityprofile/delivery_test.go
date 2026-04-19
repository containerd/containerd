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

package securityprofile

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	errNotFound    = errors.New("not found")
	errInvalidData = errors.New("invalid data")
)

func testErrors() Errors {
	return Errors{NotFound: errNotFound, InvalidData: errInvalidData}
}

func TestMaterialize_Success(t *testing.T) {
	dir := t.TempDir()
	d, err := New("io.test/", dir)
	if err != nil {
		t.Fatal(err)
	}
	content := "profile test {}"
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	labels := map[string]string{
		"io.test/myprofile": encoded,
	}
	path, changed, err := d.Materialize("myprofile", labels, testErrors())
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true on first write")
	}
	if path != filepath.Join(dir, "myprofile") {
		t.Fatalf("unexpected path: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("unexpected content: %s", string(data))
	}
}

func TestMaterialize_Idempotent(t *testing.T) {
	dir := t.TempDir()
	d, err := New("io.test/", dir)
	if err != nil {
		t.Fatal(err)
	}
	content := "profile test {}"
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	labels := map[string]string{
		"io.test/myprofile": encoded,
	}
	// First call writes
	if _, changed, err := d.Materialize("myprofile", labels, testErrors()); err != nil {
		t.Fatal(err)
	} else if !changed {
		t.Fatal("expected changed=true on first write")
	}
	// Second call is idempotent
	if _, changed, err := d.Materialize("myprofile", labels, testErrors()); err != nil {
		t.Fatal(err)
	} else if changed {
		t.Fatal("expected changed=false on second call with same content")
	}
}

func TestMaterialize_NotFound(t *testing.T) {
	dir := t.TempDir()
	d, err := New("io.test/", dir)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = d.Materialize("missing", nil, testErrors())
	if !errors.Is(err, errNotFound) {
		t.Fatalf("expected errNotFound, got: %v", err)
	}
}

func TestMaterialize_InvalidBase64(t *testing.T) {
	dir := t.TempDir()
	d, err := New("io.test/", dir)
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{
		"io.test/bad": "not-valid-base64!!!",
	}
	_, _, err = d.Materialize("bad", labels, testErrors())
	if !errors.Is(err, errInvalidData) {
		t.Fatalf("expected errInvalidData, got: %v", err)
	}
}

func TestMaterialize_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	d, err := New("io.test/", dir)
	if err != nil {
		t.Fatal(err)
	}
	content := "malicious"
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	labels := map[string]string{
		"io.test/../../../etc/passwd": encoded,
	}
	_, _, err = d.Materialize(filepath.Join("..", "..", "..", "etc", "passwd"), labels, testErrors())
	if err == nil {
		t.Fatal("expected path traversal error")
	}
}

func TestMaterialize_URLSafeBase64(t *testing.T) {
	dir := t.TempDir()
	d, err := New("io.test/", dir)
	if err != nil {
		t.Fatal(err)
	}
	content := "profile with special chars: \xff\xfe"
	encoded := base64.URLEncoding.EncodeToString([]byte(content))
	labels := map[string]string{
		"io.test/urlsafe": encoded,
	}
	path, changed, err := d.Materialize("urlsafe", labels, testErrors())
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("unexpected content: got %q, want %q", string(data), content)
	}
}

func TestMaterialize_StripTargetDirPrefix(t *testing.T) {
	dir := t.TempDir()
	d, err := New("io.test/", dir)
	if err != nil {
		t.Fatal(err)
	}
	content := "profile stripped {}"
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	labels := map[string]string{
		"io.test/myprofile": encoded,
	}
	// Pass ref with full path prefix — should be stripped
	path, _, err := d.Materialize(filepath.Join(dir, "myprofile"), labels, testErrors())
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "myprofile") {
		t.Fatalf("unexpected path: %s", path)
	}
}

func TestMaterialize_SymlinkTraversal(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	d, err := New("io.test/", dir)
	if err != nil {
		t.Fatal(err)
	}
	// Create a symlink inside targetDir pointing outside
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte("evil content"))
	labels := map[string]string{
		"io.test/escape/payload": encoded,
	}
	_, _, err = d.Materialize("escape/payload", labels, testErrors())
	if err == nil {
		t.Fatal("expected symlink traversal to be blocked")
	}
	if !strings.Contains(err.Error(), "symlink traversal") {
		t.Fatalf("unexpected error (expected symlink traversal): %v", err)
	}
	// Verify nothing was written outside
	if _, statErr := os.Stat(filepath.Join(outside, "payload")); !os.IsNotExist(statErr) {
		t.Fatal("file was written outside targetDir via symlink")
	}
}
