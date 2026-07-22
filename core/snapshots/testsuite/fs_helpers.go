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

package testsuite

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/internal/fsview"
	"github.com/containerd/containerd/v2/internal/randutil"
	erofs "github.com/erofs/go-erofs"
)

// erofsChardev is the EROFS mode type for a character device (0020000 octal),
// reproduced here to avoid importing an internal go-erofs package. Overlay
// whiteouts are character devices with rdev 0.
const erofsChardev uint16 = 0o020000

// ---------------------------------------------------------------------------
// Change model
//
// A test describes a layer's filesystem operations once as a slice of
// fsChange values. The suite then applies the same operations two ways:
//
//   - As a layer on top of a parent snapshot (overlay whiteouts for removals,
//     opaque directories for replaced directories, copy-up from the parent).
//   - As the flattened final state of all layers so far (removals actually
//     delete, renames actually move).
//
// Both applications produce a committed snapshot; their fs.FS views are then
// compared for equality. Defining the operations once removes the risk of a
// hand-written expected-state model drifting from the real mutations.
// ---------------------------------------------------------------------------

type changeKind int

const (
	changeCreateFile changeKind = iota
	changeMkdir
	changeRemove
	changeChown
	changeLink
	changeRename
)

// fsChange is a single filesystem operation within a layer.
type fsChange struct {
	kind     changeKind
	path     string
	old      string // source path for rename and link
	content  []byte
	mode     fs.FileMode
	uid, gid int
}

// fsCreateFile creates or overwrites a regular file.
func fsCreateFile(name string, content []byte, mode fs.FileMode) fsChange {
	return fsChange{kind: changeCreateFile, path: name, content: content, mode: mode}
}

// fsMkdir creates a directory.
func fsMkdir(name string, mode fs.FileMode) fsChange {
	return fsChange{kind: changeMkdir, path: name, mode: mode}
}

// fsRemove removes a file or directory (recursively).
func fsRemove(name string) fsChange {
	return fsChange{kind: changeRemove, path: name}
}

// fsChown sets ownership on an existing path.
func fsChown(name string, uid, gid int) fsChange {
	return fsChange{kind: changeChown, path: name, uid: uid, gid: gid}
}

// fsLink creates newname as a hard link to oldname.
func fsLink(oldname, newname string) fsChange {
	return fsChange{kind: changeLink, path: newname, old: oldname}
}

// fsRename moves oldname to newname.
func fsRename(oldname, newname string) fsChange {
	return fsChange{kind: changeRename, path: newname, old: oldname}
}

// ---------------------------------------------------------------------------
// In-memory model used to flatten layers into a resolved tree
// ---------------------------------------------------------------------------

// node is a single entry in the flattened filesystem model.
type node struct {
	mode     fs.FileMode
	content  []byte
	target   string
	uid, gid int
	owned    bool   // ownership was explicitly set (may be 0:0)
	linkTo   string // canonical path of the inode this hard link shares
}

// tree is the resolved state produced by replaying every layer's changes.
type tree struct {
	nodes map[string]*node // clean absolute path ("/foo") -> node
}

func newTree() *tree {
	return &tree{nodes: map[string]*node{"/": {mode: fs.ModeDir | 0o755}}}
}

func cleanAbs(p string) string {
	return path.Clean("/" + p)
}

// ensureParents creates any missing parent directories for p.
func (t *tree) ensureParents(p string) {
	for d := path.Dir(p); d != "/" && d != "."; d = path.Dir(d) {
		if _, ok := t.nodes[d]; !ok {
			t.nodes[d] = &node{mode: fs.ModeDir | 0o755}
		}
	}
}

func (t *tree) removeRecursive(p string) {
	delete(t.nodes, p)
	prefix := p + "/"
	for k := range t.nodes {
		if strings.HasPrefix(k, prefix) {
			delete(t.nodes, k)
		}
	}
}

// apply replays a single change onto the tree.
func (t *tree) apply(c fsChange) {
	p := cleanAbs(c.path)
	switch c.kind {
	case changeCreateFile:
		t.ensureParents(p)
		t.nodes[p] = &node{mode: c.mode, content: c.content}
	case changeMkdir:
		t.ensureParents(p)
		t.nodes[p] = &node{mode: c.mode | fs.ModeDir}
	case changeRemove:
		t.removeRecursive(p)
	case changeChown:
		if n := t.nodes[p]; n != nil {
			n.uid, n.gid = c.uid, c.gid
			n.owned = true
		}
	case changeLink:
		old := cleanAbs(c.old)
		if src := t.nodes[old]; src != nil {
			t.ensureParents(p)
			cp := *src
			// Both names share one inode; record the canonical target.
			if src.linkTo != "" {
				cp.linkTo = src.linkTo
			} else {
				cp.linkTo = old
			}
			t.nodes[p] = &cp
		}
	case changeRename:
		old := cleanAbs(c.old)
		src := t.nodes[old]
		if src == nil {
			return
		}
		t.ensureParents(p)
		// Move the entry and any descendants.
		moved := map[string]*node{p: src}
		oldPrefix := old + "/"
		newPrefix := p + "/"
		for k, n := range t.nodes {
			if strings.HasPrefix(k, oldPrefix) {
				moved[newPrefix+k[len(oldPrefix):]] = n
			}
		}
		t.removeRecursive(old)
		for k, n := range moved {
			t.nodes[k] = n
		}
	}
}

// flatten replays all layers in order and returns the resolved tree with hard
// link groups normalized.
func flatten(layers [][]fsChange) *tree {
	t := newTree()
	for _, layer := range layers {
		for _, c := range layer {
			t.apply(c)
		}
	}
	t.resolveLinks()
	return t
}

// resolveLinks normalizes hard link groups after flattening. Each group's
// canonical inode may have been removed or renamed, so it repoints every
// surviving member at the lexically-first survivor and clears linkTo when a
// group has a single member. This keeps the flat tree writable as a set of one
// primary node plus links.
func (t *tree) resolveLinks() {
	// Group every node by its canonical inode key, then normalize any group
	// with more than one surviving member.
	groups := map[string][]string{}
	linked := false
	for p, n := range t.nodes {
		if n.linkTo != "" {
			linked = true
		}
		groups[n.inodeKey(p)] = append(groups[n.inodeKey(p)], p)
	}
	if !linked {
		return
	}
	for _, members := range groups {
		if len(members) < 2 {
			// Single survivor (or a plain file): it owns its inode.
			t.nodes[members[0]].linkTo = ""
			continue
		}
		sort.Strings(members)
		primary := members[0]
		t.nodes[primary].linkTo = ""
		for _, m := range members[1:] {
			t.nodes[m].linkTo = primary
		}
	}
}

// inodeKey returns the canonical identifier of the inode a node belongs to.
func (n *node) inodeKey(self string) string {
	if n.linkTo != "" {
		return n.linkTo
	}
	return self
}

// sortedPaths returns all non-root paths in the tree, parents before children.
func (t *tree) sortedPaths() []string {
	ps := make([]string, 0, len(t.nodes))
	for p := range t.nodes {
		if p != "/" {
			ps = append(ps, p)
		}
	}
	sort.Strings(ps)
	return ps
}

// ---------------------------------------------------------------------------
// Layer and flat writers
//
// Emission is shared via the entrySink interface: an erofsSink writes into a
// go-erofs image while a dirSink writes into an on-disk directory. The flat
// and layered emitters are backend-agnostic and drive a sink; each snapshotter
// only differs in which sink it supplies. The native snapshotter is the one
// exception: its active directory is a real mutable copy of the parent, so it
// applies the raw changes sequentially instead (see writeNativeDir).
// ---------------------------------------------------------------------------

// concreteNode is the backend-independent description of a single filesystem
// entry to materialize (not a whiteout or hard link).
type concreteNode struct {
	mode     fs.FileMode
	content  []byte
	target   string // symlink target
	uid, gid int
	owned    bool // ownership explicitly set (apply even when 0:0)
	opaque   bool // directory hides lower-layer content (overlay opaque)
}

// entrySink materializes filesystem entries for one backend. Paths are clean
// absolute ("/foo"). Implementations copy lower-layer entries up as needed so
// that mutations and references to inherited paths resolve correctly.
type entrySink interface {
	// ensureParents materializes the ancestor directories of path.
	ensureParents(path string) error
	// node writes a concrete file, directory or symlink.
	node(path string, n concreteNode) error
	// whiteout hides a lower-layer path.
	whiteout(path string) error
	// link creates a hard link at path to the entry at target.
	link(path, target string) error
	// copyUp materializes the parent entry at src into the sink at dst.
	copyUp(src, dst string) error
	// chown mutates the ownership of an already-materialized entry.
	chown(path string, uid, gid int) error
}

// emitFlat writes a resolved tree (the flattened reference state) to a sink.
func emitFlat(sink entrySink, t *tree) error {
	for _, p := range t.sortedPaths() {
		n := t.nodes[p]
		if err := sink.ensureParents(p); err != nil {
			return err
		}
		if n.linkTo != "" {
			if err := sink.link(p, n.linkTo); err != nil {
				return fmt.Errorf("link %s -> %s: %w", p, n.linkTo, err)
			}
			continue
		}
		if err := sink.node(p, n.concrete()); err != nil {
			return err
		}
	}
	return nil
}

// emitLayer writes a resolved layer (an overlay on top of the parent) to a sink.
func emitLayer(sink entrySink, m *layerModel) error {
	for _, p := range m.sortedPaths() {
		e := m.entries[p]
		if err := sink.ensureParents(p); err != nil {
			return err
		}
		switch e.kind {
		case entryWhiteout:
			if err := sink.whiteout(p); err != nil {
				return fmt.Errorf("whiteout %s: %w", p, err)
			}

		case entryLink:
			if err := sink.link(p, e.link); err != nil {
				return fmt.Errorf("link %s -> %s: %w", p, e.link, err)
			}

		case entryCopyFrom:
			// Rename whose source is in a lower layer: materialize its content
			// at the destination.
			if err := sink.copyUp(e.link, p); err != nil {
				return err
			}

		case entryNode:
			if e.fromParent {
				// Metadata-only change to a lower-layer entry: copy up first.
				if err := sink.copyUp(p, p); err != nil {
					return err
				}
				if e.setOwner {
					if err := sink.chown(p, e.uid, e.gid); err != nil {
						return err
					}
				}
				continue
			}
			if err := sink.node(p, e.concrete()); err != nil {
				return err
			}
		}
	}
	return nil
}

// concrete converts a flattened node to a backend-independent concreteNode.
func (n *node) concrete() concreteNode {
	return concreteNode{
		mode:    n.mode,
		content: n.content,
		target:  n.target,
		uid:     n.uid,
		gid:     n.gid,
		owned:   n.owned || n.uid != 0 || n.gid != 0,
	}
}

// concrete converts a resolved layer entry to a backend-independent concreteNode.
func (e *layerEntry) concrete() concreteNode {
	return concreteNode{
		mode:    e.mode,
		content: e.content,
		target:  e.target,
		uid:     e.uid,
		gid:     e.gid,
		owned:   e.setOwner,
		opaque:  e.opaque,
	}
}

// ---------------------------------------------------------------------------
// EROFS sink
// ---------------------------------------------------------------------------

// erofsSink writes entries into a go-erofs image, copying lower-layer entries
// up from parent when they must be materialized in this layer.
type erofsSink struct {
	w       *erofs.Writer
	parent  fs.FS
	created map[string]bool
}

func newErofsSink(w *erofs.Writer, parent fs.FS) *erofsSink {
	return &erofsSink{w: w, parent: parent, created: map[string]bool{}}
}

func (s *erofsSink) ensureParents(p string) error {
	// Copy existing parent directories up with their real attributes so they
	// do not shadow the lower layer; fabricate genuinely new ancestors 0755.
	for _, d := range missingAncestors(p, func(d string) bool {
		if s.created[d] {
			return true
		}
		_, err := s.w.Stat(d)
		return err == nil
	}) {
		if s.copyUpDir(d) {
			continue
		}
		if err := s.w.Mkdir(d, 0o755); err != nil {
			return err
		}
		s.created[d] = true
	}
	return nil
}

func (s *erofsSink) node(p string, n concreteNode) error {
	switch {
	case n.mode.IsDir():
		if err := s.w.Mkdir(p, n.mode.Perm()); err != nil {
			return fmt.Errorf("mkdir %s: %w", p, err)
		}
		if n.opaque {
			if err := s.w.Setxattr(p, "trusted.overlay.opaque", "y"); err != nil {
				return err
			}
		}
	case n.mode&fs.ModeSymlink != 0:
		if err := s.w.Symlink(n.target, p); err != nil {
			return fmt.Errorf("symlink %s: %w", p, err)
		}
	default:
		f, err := s.w.Create(p)
		if err != nil {
			return fmt.Errorf("create %s: %w", p, err)
		}
		if len(n.content) > 0 {
			if _, err := f.Write(n.content); err != nil {
				f.Close()
				return fmt.Errorf("write %s: %w", p, err)
			}
		}
		if err := f.Chmod(n.mode.Perm()); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	s.created[p] = true
	if n.owned {
		return s.w.Chown(p, n.uid, n.gid)
	}
	return nil
}

func (s *erofsSink) whiteout(p string) error {
	s.created[p] = true
	return s.w.Mknod(p, erofsChardev, 0)
}

func (s *erofsSink) link(p, target string) error {
	if err := s.copyUp(target, target); err != nil {
		return err
	}
	s.created[p] = true
	return s.w.Link(target, p)
}

func (s *erofsSink) chown(p string, uid, gid int) error { return s.w.Chown(p, uid, gid) }

// copyUp materializes the parent entry at src into the writer at dst.
func (s *erofsSink) copyUp(src, dst string) error {
	if s.created[dst] {
		return nil
	}
	if _, err := s.w.Stat(dst); err == nil {
		return nil
	}
	fi, ok := statParentEntry(s.parent, src)
	if !ok {
		return nil // absent from parent; nothing to copy up
	}
	if err := s.ensureParents(dst); err != nil {
		return err
	}
	uid, gid, owned := ownerOf(fi)
	n := concreteNode{mode: fi.Mode(), uid: uid, gid: gid, owned: owned}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := readlinkParent(s.parent, strings.TrimPrefix(src, "/"))
		if err != nil {
			return err
		}
		n.target = target
	case !fi.IsDir():
		data, err := fs.ReadFile(s.parent, strings.TrimPrefix(src, "/"))
		if err != nil {
			return err
		}
		n.content = data
	}
	return s.node(dst, n)
}

// copyUpDir copies a parent directory into the writer with its real mode and
// owner, reporting whether it existed in the parent.
func (s *erofsSink) copyUpDir(d string) bool {
	fi, ok := statParentEntry(s.parent, d)
	if !ok || !fi.IsDir() {
		return false
	}
	if err := s.w.Mkdir(d, fi.Mode().Perm()); err != nil {
		return false
	}
	if uid, gid, owned := ownerOf(fi); owned {
		_ = s.w.Chown(d, uid, gid)
	}
	s.created[d] = true
	return true
}

// ---------------------------------------------------------------------------
// Per-layer resolution
//
// resolveLayer collapses an ordered change list into a single intent per path.
// This lets the EROFS writer emit each path once regardless of how many
// operations touched it in the source layer.
// ---------------------------------------------------------------------------

type entryKind int

const (
	entryNode     entryKind = iota // a concrete file/dir/symlink in this layer
	entryWhiteout                  // a deletion of a lower-layer path
	entryLink                      // a hard link to another path
	entryCopyFrom                  // a copy-up of a parent path to this path
)

type layerEntry struct {
	kind     entryKind
	mode     fs.FileMode
	content  []byte
	target   string
	link     string // source path for entryLink
	uid, gid int
	setOwner bool
	opaque   bool
	// fromParent marks an entry that only carries metadata changes to a path
	// inherited from a lower layer (needs copy-up rather than fresh creation).
	fromParent bool
}

type layerModel struct {
	entries map[string]*layerEntry
}

func (m *layerModel) sortedPaths() []string {
	ps := make([]string, 0, len(m.entries))
	for p := range m.entries {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	return ps
}

// resolveLayer replays a layer's changes into per-path intents.
func resolveLayer(changes []fsChange) *layerModel {
	m := &layerModel{entries: map[string]*layerEntry{}}
	// removedHere tracks paths removed in this layer, to detect
	// remove-then-recreate (opaque) and to know when a rename source needs a
	// whiteout.
	removedHere := map[string]bool{}

	setNode := func(p string, e *layerEntry) {
		if removedHere[p] {
			e.opaque = e.mode.IsDir()
			removedHere[p] = false
		}
		m.entries[p] = e
	}

	for _, c := range changes {
		p := cleanAbs(c.path)
		switch c.kind {
		case changeCreateFile:
			setNode(p, &layerEntry{kind: entryNode, mode: c.mode, content: c.content})
		case changeMkdir:
			setNode(p, &layerEntry{kind: entryNode, mode: c.mode | fs.ModeDir})
		case changeRemove:
			m.removeSubtree(p)
			removedHere[p] = true
			m.entries[p] = &layerEntry{kind: entryWhiteout}
		case changeChown:
			e := m.mutable(p)
			e.uid, e.gid, e.setOwner = c.uid, c.gid, true
		case changeLink:
			old := cleanAbs(c.old)
			if src := m.entries[old]; src != nil && src.kind == entryNode {
				// Source created in this layer: a true hard link works.
				setNode(p, &layerEntry{kind: entryLink, link: old})
			} else {
				// Source lives in a lower layer: copy its content up. Overlay
				// layers cannot hard-link across the layer boundary.
				setNode(p, &layerEntry{kind: entryCopyFrom, link: old})
			}
		case changeRename:
			old := cleanAbs(c.old)
			src := m.entries[old]
			// Move the resolved source entry (if created in this layer) to the
			// destination; otherwise mark the destination as a copy-up of the
			// parent path via a link-like node handled at emit time.
			if src != nil && src.kind == entryNode {
				moved := *src
				setNode(p, &moved)
				delete(m.entries, old)
			} else {
				// Source lives in a lower layer: copy it up to the destination
				// and whiteout the source.
				setNode(p, &layerEntry{kind: entryCopyFrom, link: old})
			}
			if !removedHere[old] {
				m.entries[old] = &layerEntry{kind: entryWhiteout}
				removedHere[old] = true
			}
		}
	}
	return m
}

// mutable returns the entry for p, creating a metadata-only "fromParent" node
// when the path was not otherwise touched in this layer.
func (m *layerModel) mutable(p string) *layerEntry {
	e := m.entries[p]
	if e == nil {
		e = &layerEntry{kind: entryNode, fromParent: true}
		m.entries[p] = e
	}
	return e
}

// removeSubtree drops any entries created in this layer beneath p.
func (m *layerModel) removeSubtree(p string) {
	delete(m.entries, p)
	prefix := p + "/"
	for k := range m.entries {
		if strings.HasPrefix(k, prefix) {
			delete(m.entries, k)
		}
	}
}

// missingAncestors returns the ancestor directories of p that need to be
// created, ordered top-down (shallowest first). exists reports whether an
// ancestor is already present.
func missingAncestors(p string, exists func(string) bool) []string {
	var missing []string
	for d := path.Dir(p); d != "/" && d != "."; d = path.Dir(d) {
		if exists(d) {
			break
		}
		missing = append(missing, d)
	}
	// Reverse into top-down order.
	for i, j := 0, len(missing)-1; i < j; i, j = i+1, j-1 {
		missing[i], missing[j] = missing[j], missing[i]
	}
	return missing
}

// statParentEntry stats a path (relative or absolute) in the parent view
// without following a final symlink. It reports false when there is no parent
// or the path is absent.
func statParentEntry(parent fs.FS, p string) (fs.FileInfo, bool) {
	if parent == nil {
		return nil, false
	}
	rel := strings.TrimPrefix(p, "/")
	var (
		fi  fs.FileInfo
		err error
	)
	if rl, ok := parent.(fs.ReadLinkFS); ok {
		fi, err = rl.Lstat(rel)
	} else {
		fi, err = fs.Stat(parent, rel)
	}
	if err != nil {
		return nil, false
	}
	return fi, true
}

func readlinkParent(parent fs.FS, rel string) (string, error) {
	if rl, ok := parent.(fs.ReadLinkFS); ok {
		return rl.ReadLink(rel)
	}
	return "", fmt.Errorf("parent does not support readlink")
}

// ---------------------------------------------------------------------------
// Directory sink (overlay upperdir)
// ---------------------------------------------------------------------------

// dirSink writes entries into an overlay upperdir on disk, using character
// device whiteouts and opaque xattrs, and copying lower-layer entries up
// before mutating or referencing them.
type dirSink struct {
	dir    string
	parent fs.FS
}

func newDirSink(dir string, parent fs.FS) *dirSink {
	return &dirSink{dir: dir, parent: parent}
}

func (s *dirSink) target(p string) string {
	return filepath.Join(s.dir, filepath.FromSlash(strings.TrimPrefix(p, "/")))
}

func (s *dirSink) ensureParents(p string) error {
	for _, d := range missingAncestors(p, func(d string) bool {
		_, err := os.Lstat(s.target(d))
		return err == nil
	}) {
		if err := s.copyUp(d, d); err != nil {
			return err
		}
		if _, err := os.Lstat(s.target(d)); err != nil {
			if err := os.Mkdir(s.target(d), 0o755); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *dirSink) node(p string, n concreteNode) error {
	target := s.target(p)
	switch {
	case n.mode.IsDir():
		if err := os.MkdirAll(target, n.mode.Perm()); err != nil {
			return err
		}
		if err := os.Chmod(target, n.mode.Perm()); err != nil {
			return err
		}
		if n.opaque {
			if err := setOpaque(target); err != nil {
				return err
			}
		}
	case n.mode&fs.ModeSymlink != 0:
		if err := os.Symlink(n.target, target); err != nil {
			return err
		}
	default:
		if err := os.WriteFile(target, n.content, n.mode.Perm()); err != nil {
			return err
		}
		if err := os.Chmod(target, n.mode.Perm()); err != nil {
			return err
		}
	}
	if n.owned {
		return os.Lchown(target, n.uid, n.gid)
	}
	return nil
}

func (s *dirSink) whiteout(p string) error { return mknodChar(s.target(p)) }
func (s *dirSink) link(p, target string) error {
	return os.Link(s.target(target), s.target(p))
}
func (s *dirSink) chown(p string, uid, gid int) error { return os.Lchown(s.target(p), uid, gid) }

func (s *dirSink) copyUp(src, dst string) error {
	target := s.target(dst)
	if _, err := os.Lstat(target); err == nil {
		return nil
	}
	fi, ok := statParentEntry(s.parent, src)
	if !ok {
		return nil
	}
	uid, gid, owned := ownerOf(fi)
	n := concreteNode{mode: fi.Mode(), uid: uid, gid: gid, owned: owned}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		t, err := readlinkParent(s.parent, strings.TrimPrefix(src, "/"))
		if err != nil {
			return err
		}
		n.target = t
	case !fi.IsDir():
		data, err := fs.ReadFile(s.parent, strings.TrimPrefix(src, "/"))
		if err != nil {
			return err
		}
		n.content = data
	}
	return s.node(dst, n)
}

// writeNativeDir applies a layer's changes to a native snapshot directory,
// which already holds a copy of the parent. Because the directory is a real
// mutable tree, the raw changes are applied sequentially: removals delete,
// renames move, and links hard-link, all in place.
func writeNativeDir(dir string, changes []fsChange) error {
	targetOf := func(p string) string {
		return filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(cleanAbs(p), "/")))
	}
	for _, c := range changes {
		target := targetOf(c.path)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		var err error
		switch c.kind {
		case changeCreateFile:
			if err = os.WriteFile(target, c.content, c.mode.Perm()); err == nil {
				err = os.Chmod(target, c.mode.Perm())
			}
		case changeMkdir:
			if err = os.MkdirAll(target, c.mode.Perm()); err == nil {
				err = os.Chmod(target, c.mode.Perm())
			}
		case changeRemove:
			err = os.RemoveAll(target)
		case changeChown:
			err = os.Lchown(target, c.uid, c.gid)
		case changeLink:
			err = os.Link(targetOf(c.old), target)
		case changeRename:
			err = os.Rename(targetOf(c.old), target)
		}
		if err != nil {
			return fmt.Errorf("native %s: %w", c.path, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Snapshot creation via the snapshotter under test
// ---------------------------------------------------------------------------

// fsCommitNamed prepares an active snapshot under activeKey (on optional
// parent), applies changes, and commits it under committedName. Callers that
// need specific snapshot names (Walk, Update, etc.) use this. commitOpt
// overrides the default gc-root option when provided.
func fsCommitNamed(ctx context.Context, sn snapshots.Snapshotter, work, activeKey, committedName, parent string, commitOpt ...snapshots.Opt) error {
	if len(commitOpt) == 0 {
		commitOpt = []snapshots.Opt{fsOpt}
	}
	mounts, err := sn.Prepare(ctx, activeKey, parent, fsOpt)
	if err != nil {
		return fmt.Errorf("prepare %s: %w", activeKey, err)
	}
	if err := applyLayer(mounts, nil); err != nil {
		_ = sn.Remove(ctx, activeKey)
		return fmt.Errorf("apply %s: %w", activeKey, err)
	}
	if err := sn.Commit(ctx, committedName, activeKey, commitOpt...); err != nil {
		return fmt.Errorf("commit %s: %w", committedName, err)
	}
	return nil
}

// fsCreateLayer prepares an active snapshot on top of parent, applies the
// layer's changes using the strategy that matches the snapshotter, and commits
// it. It returns the committed snapshot name.
func fsCreateLayer(ctx context.Context, sn snapshots.Snapshotter, parent, work string, changes []fsChange) (string, error) {
	name := fmt.Sprintf("fs-%d", randutil.Int())
	prepare := name + "-prepare"

	mounts, err := sn.Prepare(ctx, prepare, parent, fsOpt)
	if err != nil {
		return "", fmt.Errorf("prepare: %w", err)
	}

	if err := applyLayer(mounts, changes); err != nil {
		_ = sn.Remove(ctx, prepare)
		return "", fmt.Errorf("apply layer: %w", err)
	}
	if err := sn.Commit(ctx, name, prepare, fsOpt); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return name, nil
}

// snapshotTarget classifies an active snapshot's mounts into the write
// strategy that fits the snapshotter that produced them.
type snapshotTarget struct {
	kind        targetKind
	dir         string // upperdir (overlay) or source dir (native)
	snapshotDir string // erofs snapshot directory
}

type targetKind int

const (
	targetErofs targetKind = iota
	targetOverlay
	targetNative
)

// classifyTarget determines how to write to the active snapshot.
func classifyTarget(mounts []mount.Mount) (snapshotTarget, error) {
	if len(mounts) == 0 {
		return snapshotTarget{}, fmt.Errorf("no mounts to apply to")
	}
	last := mounts[len(mounts)-1]

	switch {
	case last.Type == "bind" || last.Type == "rbind":
		snapshotDir := filepath.Dir(last.Source)
		if isErofsSnapshot(snapshotDir) {
			return snapshotTarget{kind: targetErofs, snapshotDir: snapshotDir}, nil
		}
		return snapshotTarget{kind: targetNative, dir: last.Source}, nil

	case last.Type == "overlay":
		dir, ok := upperdir(last.Options)
		if !ok {
			return snapshotTarget{}, fmt.Errorf("overlay mount has no upperdir option")
		}
		return snapshotTarget{kind: targetOverlay, dir: dir}, nil

	case strings.HasPrefix(last.Type, "format/") && strings.HasSuffix(last.Type, "overlay"):
		if snapshotDir, ok := erofsSnapshotDir(last.Options); ok && isErofsSnapshot(snapshotDir) {
			return snapshotTarget{kind: targetErofs, snapshotDir: snapshotDir}, nil
		}
		if dir, ok := upperdir(last.Options); ok && !strings.Contains(dir, "{{") {
			return snapshotTarget{kind: targetOverlay, dir: dir}, nil
		}
		return snapshotTarget{}, fmt.Errorf("unsupported format overlay options: %v", last.Options)

	default:
		return snapshotTarget{}, fmt.Errorf("unsupported mount type: %s", last.Type)
	}
}

// lowerView returns a read-only fs.FS of the parent (lower) layers embedded in
// an active snapshot's mounts, for copy-up. The parent stack is already part of
// the active mounts, so instead of preparing a separate parent View we strip
// the writable upper from the final overlay mount and open what remains. It
// returns (nil, nil) when the mounts carry no lower layers (e.g. a base layer
// or the native copy, which needs no copy-up).
func lowerView(mounts []mount.Mount) (fs.FS, func(), error) {
	if len(mounts) == 0 {
		return nil, nil, nil
	}
	last := mounts[len(mounts)-1]

	// Only overlay-style mounts embed a separate lower stack. Bind mounts
	// (native, or an erofs base layer) have no lower to view.
	isOverlay := last.Type == "overlay" ||
		(strings.HasPrefix(last.Type, "format/") && strings.HasSuffix(last.Type, "overlay"))
	if !isOverlay {
		return nil, nil, nil
	}

	lower := stripWritableUpper(mounts)
	if lower == nil {
		return nil, nil, nil // no lowerdir: nothing to copy up from
	}

	v, err := fsview.FSMounts(lower)
	if err != nil {
		return nil, nil, fmt.Errorf("open lower view: %w", err)
	}
	if v == nil {
		return nil, nil, nil
	}
	return v, func() { v.Close() }, nil
}

// stripWritableUpper returns a copy of mounts with the final overlay mount's
// writable upperdir and workdir removed, leaving only its lowerdir. It returns
// nil when the final mount has no lowerdir (no parent layers).
func stripWritableUpper(mounts []mount.Mount) []mount.Mount {
	last := mounts[len(mounts)-1]

	var kept []string
	hasLower := false
	for _, o := range last.Options {
		switch {
		case strings.HasPrefix(o, "upperdir="),
			strings.HasPrefix(o, "workdir="),
			strings.HasPrefix(o, "X-containerd.mkdir."):
			// Drop the writable-upper wiring.
		case strings.HasPrefix(o, "lowerdir="):
			hasLower = true
			kept = append(kept, o)
		default:
			kept = append(kept, o)
		}
	}
	if !hasLower {
		return nil
	}

	out := make([]mount.Mount, len(mounts))
	copy(out, mounts)
	lastCopy := last
	lastCopy.Options = kept
	out[len(out)-1] = lastCopy
	return out
}

// applyLayer writes changes to the active snapshot as an overlay on top of the
// parent layers embedded in its own mounts. The parent (lower) view used for
// copy-up is derived from those mounts rather than a separate View. The erofs
// and overlay backends share the resolved-layer emitter via a sink; native
// applies the raw changes directly to its parent copy.
func applyLayer(mounts []mount.Mount, changes []fsChange) error {
	tgt, err := classifyTarget(mounts)
	if err != nil {
		return err
	}
	switch tgt.kind {
	case targetErofs, targetOverlay:
		parentView, closeParent, err := lowerView(mounts)
		if err != nil {
			return err
		}
		if closeParent != nil {
			defer closeParent()
		}
		if tgt.kind == targetErofs {
			return buildErofsImage(tgt.snapshotDir, func(w *erofs.Writer) error {
				return emitLayer(newErofsSink(w, parentView), resolveLayer(changes))
			})
		}
		return emitLayer(newDirSink(tgt.dir, parentView), resolveLayer(changes))
	case targetNative:
		return writeNativeDir(tgt.dir, changes)
	}
	return fmt.Errorf("unhandled target kind")
}

// buildErofsImage writes the snapshot's layer.erofs using the provided writer
// callback.
func buildErofsImage(snapshotDir string, write func(*erofs.Writer) error) error {
	layerPath := filepath.Join(snapshotDir, "layer.erofs")
	f, err := os.Create(layerPath)
	if err != nil {
		return fmt.Errorf("create layer.erofs: %w", err)
	}
	defer f.Close()

	w := erofs.Create(f)
	if err := write(w); err != nil {
		return err
	}
	return w.Close()
}

func isErofsSnapshot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".erofslayer"))
	return err == nil
}

func upperdir(options []string) (string, bool) {
	for _, o := range options {
		if v, ok := strings.CutPrefix(o, "upperdir="); ok {
			return v, true
		}
	}
	return "", false
}

func erofsSnapshotDir(options []string) (string, bool) {
	for _, o := range options {
		if v, ok := strings.CutPrefix(o, "workdir="); ok && !strings.Contains(v, "{{") {
			return filepath.Dir(v), true
		}
		if v, ok := strings.CutPrefix(o, "upperdir="); ok && !strings.Contains(v, "{{") {
			return filepath.Dir(v), true
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Verification driver
// ---------------------------------------------------------------------------

// fsCheckSnapshots builds a layered snapshot chain with the snapshotter under
// test and, for each prefix of layers, compares its committed view against an
// independent canonical EROFS image built directly from the flattened changes.
// Using a snapshotter-independent reference means the test validates the
// snapshotter against a known-good state rather than against itself.
func fsCheckSnapshots(ctx context.Context, sn snapshots.Snapshotter, work string, layers ...[]fsChange) error {
	parent := ""
	for i := range layers {
		layered, err := fsCreateLayer(ctx, sn, parent, work, layers[i])
		if err != nil {
			return fmt.Errorf("layer %d: %w", i+1, err)
		}

		ref, closeRef, err := openFlatReference(work, layers[:i+1])
		if err != nil {
			return fmt.Errorf("reference %d: %w", i+1, err)
		}

		got, closeGot, err := openCommittedView(ctx, sn, layered)
		if err != nil {
			closeRef()
			return err
		}
		err = compareFS(got, ref)
		closeGot()
		closeRef()
		if err != nil {
			return fmt.Errorf("compare at layer %d: %w", i+1, err)
		}
		parent = layered
	}
	return nil
}

// openFlatReference builds the canonical flattened filesystem as a standalone
// EROFS image and opens it read-only. It does not use the snapshotter, so it
// serves as an independent oracle for comparison.
func openFlatReference(work string, layers [][]fsChange) (fs.FS, func(), error) {
	f, err := os.CreateTemp(work, "flat-*.erofs")
	if err != nil {
		return nil, nil, fmt.Errorf("create reference image: %w", err)
	}
	name := f.Name()

	w := erofs.Create(f)
	if err := emitFlat(newErofsSink(w, nil), flatten(layers)); err != nil {
		f.Close()
		_ = os.Remove(name)
		return nil, nil, fmt.Errorf("build reference image: %w", err)
	}
	if err := w.Close(); err != nil { // serializes the image; does not close f
		f.Close()
		_ = os.Remove(name)
		return nil, nil, fmt.Errorf("finalize reference image: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return nil, nil, fmt.Errorf("close reference image: %w", err)
	}

	rf, err := os.Open(name)
	if err != nil {
		_ = os.Remove(name)
		return nil, nil, fmt.Errorf("open reference image: %w", err)
	}
	efs, err := erofs.Open(rf)
	if err != nil {
		rf.Close()
		_ = os.Remove(name)
		return nil, nil, fmt.Errorf("read reference image: %w", err)
	}
	return efs, func() {
		rf.Close()
		_ = os.Remove(name)
	}, nil
}

// openCommittedView creates a temporary view of a committed snapshot and opens
// it as an fs.FS.
func openCommittedView(ctx context.Context, sn snapshots.Snapshotter, name string) (fs.FS, func(), error) {
	viewKey := fmt.Sprintf("%s-view-%d", name, randutil.Int())
	mounts, err := sn.View(ctx, viewKey, name, fsOpt)
	if err != nil {
		return nil, nil, fmt.Errorf("view %s: %w", name, err)
	}
	v, err := fsview.FSMounts(mounts)
	if err != nil {
		_ = sn.Remove(ctx, viewKey)
		return nil, nil, fmt.Errorf("FSMounts %s: %w", name, err)
	}
	if v == nil {
		_ = sn.Remove(ctx, viewKey)
		return nil, nil, fmt.Errorf("FSMounts %s returned nil", name)
	}
	return v, func() {
		v.Close()
		_ = sn.Remove(ctx, viewKey)
	}, nil
}

// fsMountsView opens mounts as an fs.FS view, returning a close function.
func fsMountsView(mounts []mount.Mount) (fs.FS, func() error, error) {
	v, err := fsview.FSMounts(mounts)
	if err != nil {
		return nil, nil, err
	}
	if v == nil {
		return nil, nil, errors.New("FSMounts returned nil")
	}
	return v, v.Close, nil
}

// ---------------------------------------------------------------------------
// fs.FS comparison
// ---------------------------------------------------------------------------

// compareFS asserts that two filesystems are structurally identical: same set
// of paths, and for each path the same mode (type and permission bits),
// content, symlink target, ownership and xattrs. Modification times are not
// compared as they are not meaningfully preserved across snapshotter formats.
func compareFS(got, want fs.FS) error {
	gotEntries, err := walkFS(got)
	if err != nil {
		return fmt.Errorf("walk got: %w", err)
	}
	wantEntries, err := walkFS(want)
	if err != nil {
		return fmt.Errorf("walk want: %w", err)
	}

	for p := range wantEntries {
		if _, ok := gotEntries[p]; !ok {
			return fmt.Errorf("missing entry %q", p)
		}
	}
	for p := range gotEntries {
		if _, ok := wantEntries[p]; !ok {
			return fmt.Errorf("unexpected entry %q", p)
		}
	}

	paths := make([]string, 0, len(wantEntries))
	for p := range wantEntries {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		g, w := gotEntries[p], wantEntries[p]
		if g.Mode().Type() != w.Mode().Type() {
			return fmt.Errorf("%s: type %v != %v", p, g.Mode().Type(), w.Mode().Type())
		}
		if g.Mode().Perm() != w.Mode().Perm() {
			return fmt.Errorf("%s: perm %o != %o", p, g.Mode().Perm(), w.Mode().Perm())
		}
		if g.Mode().IsRegular() {
			gd, err := fs.ReadFile(got, p)
			if err != nil {
				return fmt.Errorf("%s: read got: %w", p, err)
			}
			wd, err := fs.ReadFile(want, p)
			if err != nil {
				return fmt.Errorf("%s: read want: %w", p, err)
			}
			if string(gd) != string(wd) {
				return fmt.Errorf("%s: content %q != %q", p, gd, wd)
			}
		}
		if g.Mode()&fs.ModeSymlink != 0 {
			gt, err := readlink(got, p)
			if err != nil {
				return fmt.Errorf("%s: readlink got: %w", p, err)
			}
			wt, err := readlink(want, p)
			if err != nil {
				return fmt.Errorf("%s: readlink want: %w", p, err)
			}
			if gt != wt {
				return fmt.Errorf("%s: symlink target %q != %q", p, gt, wt)
			}
		}
		if err := compareOwner(p, g, w); err != nil {
			return err
		}
		if err := compareXattrs(p, g, w); err != nil {
			return err
		}
	}
	return nil
}

// walkFS returns a map of every path (excluding ".") in fsys to its
// FileInfo, without following symlinks.
func walkFS(fsys fs.FS) (map[string]fs.FileInfo, error) {
	out := map[string]fs.FileInfo{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		fi, err := lstatFS(fsys, p)
		if err != nil {
			return fmt.Errorf("lstat %s: %w", p, err)
		}
		out[p] = fi
		return nil
	})
	return out, err
}

func lstatFS(fsys fs.FS, name string) (fs.FileInfo, error) {
	if rl, ok := fsys.(fs.ReadLinkFS); ok {
		return rl.Lstat(name)
	}
	return fs.Stat(fsys, name)
}

func readlink(fsys fs.FS, name string) (string, error) {
	if rl, ok := fsys.(fs.ReadLinkFS); ok {
		return rl.ReadLink(name)
	}
	return "", fmt.Errorf("filesystem does not support readlink")
}

// ownerOf extracts UID/GID from a FileInfo produced by an erofs image or a
// host directory (syscall.Stat_t).
func ownerOf(fi fs.FileInfo) (uid, gid int, ok bool) {
	switch st := fi.Sys().(type) {
	case *erofs.Stat:
		return int(st.UID), int(st.GID), true
	case *erofs.WriterStat:
		return int(st.UID), int(st.GID), true
	case *syscall.Stat_t:
		return int(st.Uid), int(st.Gid), true
	}
	return 0, 0, false
}

func compareOwner(p string, g, w fs.FileInfo) error {
	guid, ggid, gok := ownerOf(g)
	wuid, wgid, wok := ownerOf(w)
	if gok != wok {
		return fmt.Errorf("%s: ownership exposed on one side only (got=%v want=%v)", p, gok, wok)
	}
	if !gok {
		return nil // neither side exposes ownership
	}
	if guid != wuid || ggid != wgid {
		return fmt.Errorf("%s: owner %d:%d != %d:%d", p, guid, ggid, wuid, wgid)
	}
	return nil
}

// xattrsOf returns the extended attributes of fi and whether its FileInfo
// exposes them. Only erofs-backed views surface xattrs through Sys(); a
// directory-backed view (syscall.Stat_t) does not, so ok is false there.
func xattrsOf(fi fs.FileInfo) (map[string]string, bool) {
	switch st := fi.Sys().(type) {
	case *erofs.Stat:
		return st.Xattrs, true
	case *erofs.WriterStat:
		return st.Xattrs, true
	}
	return nil, false
}

func compareXattrs(p string, g, w fs.FileInfo) error {
	gx, gok := xattrsOf(g)
	wx, wok := xattrsOf(w)
	// Only compare when both sides expose xattrs. Directory-backed views
	// (overlay, native) do not surface xattrs through Sys(), so the check is
	// skipped rather than reported as a mismatch.
	if !gok || !wok {
		return nil
	}
	gu := userXattrs(gx)
	wu := userXattrs(wx)
	if len(gu) != len(wu) {
		return fmt.Errorf("%s: xattr count %d != %d", p, len(gu), len(wu))
	}
	for k, v := range wu {
		if gu[k] != v {
			return fmt.Errorf("%s: xattr %q = %q != %q", p, k, gu[k], v)
		}
	}
	return nil
}

// userXattrs filters out overlay-internal xattrs (e.g. the opaque marker) that
// are an artifact of layering rather than user-visible metadata.
func userXattrs(x map[string]string) map[string]string {
	if len(x) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range x {
		if strings.HasPrefix(k, "trusted.overlay.") || strings.HasPrefix(k, "user.overlay.") {
			continue
		}
		out[k] = v
	}
	return out
}
