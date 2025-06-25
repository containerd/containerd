// Copyright 2023 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fs

import (
	"fmt"
	"strings"
)

type childEntry struct {
	Name  string
	Inode *Inode

	// TODO: store int64 changeCounter of the parent, so we can
	// use the changeCounter as a directory offset.
}

// inodeChildren is a hashmap with deterministic ordering. It is
// important to return the children in a deterministic order for 2
// reasons:
//
// 1. if the ordering is non-deterministic, multiple concurrent
// readdirs can lead to cache corruption (see issue #391)
//
// 2. it simplifies the implementation of directory seeking: the NFS
// protocol doesn't open and close directories. Instead, a directory
// read must always be continued from a previously handed out offset.
//
// By storing the entries in insertion order, and marking them with a
// int64 logical timestamp, the logical timestamp can serve as readdir
// cookie.
type inodeChildren struct {
	// index into children slice.
	childrenMap map[string]int
	children    []childEntry
}

func (c *inodeChildren) init() {
	c.childrenMap = make(map[string]int)
}

func (c *inodeChildren) String() string {
	var ss []string
	for _, e := range c.children {
		ch := e.Inode
		ss = append(ss, fmt.Sprintf("%q=i%d[%s]", e.Name, ch.stableAttr.Ino, modeStr(ch.stableAttr.Mode)))
	}
	return strings.Join(ss, ",")
}

func (c *inodeChildren) get(name string) *Inode {
	idx, ok := c.childrenMap[name]
	if !ok {
		return nil
	}

	return c.children[idx].Inode
}

func (c *inodeChildren) compact() {
	nc := make([]childEntry, 0, 2*len(c.childrenMap)+1)
	nm := make(map[string]int, len(c.childrenMap))
	for _, e := range c.children {
		if e.Inode == nil {
			continue
		}
		nm[e.Name] = len(nc)
		nc = append(nc, e)
	}

	c.childrenMap = nm
	c.children = nc
}

func (c *inodeChildren) set(parent *Inode, name string, ch *Inode) {
	idx, ok := c.childrenMap[name]
	if !ok {
		if cap(c.children) == len(c.children) {
			c.compact()
		}

		idx = len(c.children)
		c.children = append(c.children, childEntry{})
	}

	c.childrenMap[name] = idx
	c.children[idx] = childEntry{Name: name, Inode: ch}
	parent.changeCounter++

	ch.parents.add(parentData{name, parent})
	ch.changeCounter++
}

func (c *inodeChildren) len() int {
	return len(c.childrenMap)
}

func (c *inodeChildren) toMap() map[string]*Inode {
	r := make(map[string]*Inode, len(c.childrenMap))
	for _, e := range c.children {
		if e.Inode != nil {
			r[e.Name] = e.Inode
		}
	}
	return r
}

func (c *inodeChildren) del(parent *Inode, name string) {
	idx, ok := c.childrenMap[name]
	if !ok {
		return
	}

	ch := c.children[idx].Inode

	delete(c.childrenMap, name)
	c.children[idx] = childEntry{}
	ch.parents.delete(parentData{name, parent})
	ch.changeCounter++
	parent.changeCounter++
}

func (c *inodeChildren) list() []childEntry {
	r := make([]childEntry, 0, len(c.childrenMap))
	for _, e := range c.children {
		if e.Inode != nil {
			r = append(r, e)
		}
	}
	return r
}
