// Copyright 2019 Cloudbase Solutions SRL
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package hivex

/*
#cgo CFLAGS: -g -Wall
#cgo LDFLAGS: -L/usr/local/lib -lhivex
#include <stdlib.h>
#include <errno.h>
#include <string.h>
#include "hivex.h"

size_t memberSize(hive_node_h *r) {
	size_t i, len = 0;
	for (i = 0; r[i] != 0; ++i) len++;
	return len;
}

size_t memberSizeValue(hive_value_h *r) {
	size_t i, len = 0;
	for (i = 0; r[i] != 0; ++i) len++;
	return len;
}

size_t stringMembersSize(char **r) {
	size_t i, len = 0;
    for (i = 0; r[i] != NULL; ++i) len++;
	return len;
}

char *memberValue(char **r, size_t idx) {
	char *v = malloc(sizeof(char)*strlen(r[idx]));
	memcpy(v, r[idx], strlen(r[idx]));
	return v;
}

int set_values(hive_set_value *vals, int idx, int size, hive_set_value converted) {
	if (idx > size-1) {
		return -1;
	}
	vals[idx].t = converted.t;
	vals[idx].len = converted.len;
	vals[idx].key = converted.key;
	vals[idx].value = converted.value;
	return 0;
}

*/
import "C"
import (
	"fmt"
	"unsafe"
)

const (
	// READ opens the hive as readonly
	READ = 0
	// VERBOSE instructs hivex to open the registry hive verbosely
	VERBOSE = C.HIVEX_OPEN_VERBOSE
	// DEBUG enables debug
	DEBUG = C.HIVEX_OPEN_DEBUG
	// WRITE opens the hive in write mode
	WRITE = C.HIVEX_OPEN_WRITE
	// UNSAFE enables heuristics to allow read/write of corrupted hives
	UNSAFE = C.HIVEX_OPEN_UNSAFE
)

// Constants copied over from hivex
const (
	// RegNone just a key without a value
	RegNone = C.hive_t_REG_NONE
	// RegSz a Windows string (encoding is unknown, but often UTF16-LE)
	RegSz = C.hive_t_REG_SZ
	// RegExpandSz a Windows string that contains %env%
	// (environment variable expansion)
	RegExpandSz = C.hive_t_REG_EXPAND_SZ
	// RegBinary a blob of binary
	RegBinary = C.hive_t_REG_BINARY
	// RegDword (32 bit integer), big endian
	RegDword = C.hive_t_REG_DWORD
	// RegDwordBigEndian (32 bit integer), big endian
	RegDwordBigEndian = C.hive_t_REG_DWORD_BIG_ENDIAN
	// RegLink Symbolic link to another part of the registry tree
	RegLink = C.hive_t_REG_LINK
	// RegMultiSz Multiple Windows strings.
	// See http://blogs.msdn.com/oldnewthing/archive/2009/10/08/9904646.aspx
	RegMultiSz = C.hive_t_REG_MULTI_SZ
	// RegResourceList resource list
	RegResourceList = C.hive_t_REG_RESOURCE_LIST
	// RegFullResourceDescriptor resource descriptor
	RegFullResourceDescriptor = C.hive_t_REG_FULL_RESOURCE_DESCRIPTOR
	// RegResourceRequirementsList resouce requirements list
	RegResourceRequirementsList = C.hive_t_REG_RESOURCE_REQUIREMENTS_LIST
	// RegQword (64 bit integer), unspecified endianness but usually little endian
	RegQword = C.hive_t_REG_QWORD
)

// NewHivex returns a new *Hivex instance
func NewHivex(file string, flags int) (*Hivex, error) {
	if file == "" {
		return nil, fmt.Errorf("empty filename")
	}
	fn := C.CString(file)
	var han *C.hive_h
	han, err := C.hivex_open(fn, C.int(flags))
	if err != nil {
		return nil, err
	}
	return &Hivex{
		han:  han,
		file: fn,
	}, nil
}

// Hivex implements the hivex bindings in go
type Hivex struct {
	han  *C.hive_h
	file *C.char
}

// LastModified returns the last modified time for this hive
func (h *Hivex) LastModified() (int64, error) {
	last, err := C.hivex_last_modified(h.han)
	if err != nil {
		return 0, err
	}
	return int64(last), nil
}

// Close closes the hive
func (h *Hivex) Close() error {
	// free the memory held by the filename
	defer C.free(unsafe.Pointer(h.file))
	_, err := C.hivex_close(h.han)
	if err != nil {
		return err
	}
	return nil
}

// Root returns the handle for the root of the hive
func (h *Hivex) Root() (int64, error) {
	root, err := C.hivex_root(h.han)
	if err != nil {
		return 0, err
	}
	return int64(root), nil
}

// NodeName returns the name of the specified node
func (h *Hivex) NodeName(node int64) (string, error) {
	n := (C.hive_node_h)(node)
	name, err := C.hivex_node_name(h.han, n)
	if err != nil {
		return "", err
	}
	nameLen, err := C.hivex_node_name_len(h.han, n)
	if err != nil {
		return "", err
	}
	return C.GoStringN(name, C.int(nameLen)), nil
}

// NodeNameLen returns the node name length
func (h *Hivex) NodeNameLen(node int64) (int64, error) {
	n := (C.hive_node_h)(node)
	ret, err := C.hivex_node_name_len(h.han, n)
	if err != nil {
		return 0, err
	}
	return int64(ret), nil
}

// NodeTimestamp returns the node timestamp
func (h *Hivex) NodeTimestamp(node int64) (int64, error) {
	n := (C.hive_node_h)(node)
	ret, err := C.hivex_node_timestamp(h.han, n)
	if err != nil {
		return 0, err
	}
	return int64(ret), nil
}

// NodeChildren returns a list of node children
func (h *Hivex) NodeChildren(node int64) ([]int64, error) {
	n := (C.hive_node_h)(node)
	var ret *C.hive_node_h
	var err error
	ret, err = C.hivex_node_children(h.han, n)
	if err != nil {
		return nil, err
	}
	nn := C.memberSize(ret)
	if nn == 0 {
		return []int64{}, nil
	}
	dest := make([]int64, nn)
	// nn is a size_t, so we can just get the Sizeof(nn)
	// and multiply by the number of elements it represents.
	C.memcpy(
		unsafe.Pointer(&dest[0]),
		unsafe.Pointer(ret),
		C.size_t(int64(unsafe.Sizeof(nn))*int64(nn)))
	return dest, nil
}

// NodeGetChild gets a particular child of this node
func (h *Hivex) NodeGetChild(node int64, name string) (int64, error) {
	n := (C.hive_node_h)(node)
	namePtr := C.CString(name)
	defer C.free(unsafe.Pointer(namePtr))

	ret, err := C.hivex_node_get_child(h.han, n, namePtr)
	if err != nil {
		return 0, err
	}
	return int64(ret), nil
}

// NodeNrChildren returns the number of child nodes
func (h *Hivex) NodeNrChildren(node int64) (int64, error) {
	n := (C.hive_node_h)(node)

	ret, err := C.hivex_node_nr_children(h.han, n)
	if err != nil {
		return 0, err
	}
	return int64(ret), nil
}

// NodeParent returns the parent node
func (h *Hivex) NodeParent(node int64) (int64, error) {
	n := (C.hive_node_h)(node)

	ret, err := C.hivex_node_parent(h.han, n)
	if err != nil {
		return 0, err
	}
	return int64(ret), nil
}

// NodeValues returns a list of values set for this node
func (h *Hivex) NodeValues(node int64) ([]int64, error) {
	n := (C.hive_node_h)(node)

	ret, err := C.hivex_node_values(h.han, n)
	if err != nil {
		return []int64{}, err
	}
	nn := C.memberSizeValue(ret)
	if nn == 0 {
		return []int64{}, nil
	}
	dest := make([]int64, nn)
	C.memcpy(unsafe.Pointer(&dest[0]), unsafe.Pointer(ret), C.size_t(int64(unsafe.Sizeof(nn))*int64(nn)))
	return dest, nil
}

// NodeGetValue gets the value of a node
func (h *Hivex) NodeGetValue(node int64, name string) (int64, error) {
	n := (C.hive_node_h)(node)
	namePtr := C.CString(name)
	defer C.free(unsafe.Pointer(namePtr))

	ret, err := C.hivex_node_get_value(h.han, n, namePtr)
	if err != nil {
		return 0, err
	}
	return int64(ret), nil
}

// NodeNrValues gets the nr of values of a node
func (h *Hivex) NodeNrValues(node int64) (int64, error) {
	n := (C.hive_node_h)(node)

	ret, err := C.hivex_node_nr_values(h.han, n)
	if err != nil {
		return 0, err
	}
	return int64(ret), nil
}

// NodeValueKeyLen returns the value key length
func (h *Hivex) NodeValueKeyLen(value int64) (int64, error) {
	n := (C.hive_value_h)(value)

	ret, err := C.hivex_value_key_len(h.han, n)
	if err != nil {
		return 0, err
	}
	return int64(ret), nil
}

// NodeValueKey returns the value key
func (h *Hivex) NodeValueKey(value int64) (string, error) {
	n := (C.hive_value_h)(value)

	valKey, err := C.hivex_value_key(h.han, n)
	if err != nil {
		return "", err
	}

	valLen, err := C.hivex_value_key_len(h.han, n)
	if err != nil {
		return "", err
	}
	return C.GoStringN(valKey, C.int(valLen)), nil
}

// NodeValueType returns the value type
func (h *Hivex) NodeValueType(value int64) (valueType, length int64, err error) {
	n := (C.hive_value_h)(value)
	var t C.hive_type
	var l C.size_t
	_, err = C.hivex_value_type(h.han, n, &t, &l)
	if err != nil {
		return
	}
	valueType = int64(t)
	length = int64(l)
	return
}

// NodeStructLength returns the node struct length
func (h *Hivex) NodeStructLength(node int64) (int64, error) {
	n := (C.hive_node_h)(node)

	ret, err := C.hivex_node_struct_length(h.han, n)
	if err != nil {
		return 0, err
	}
	return int64(ret), nil
}

// NodeValueStructLength returns the length of the value struct
func (h *Hivex) NodeValueStructLength(value int64) (int64, error) {
	n := (C.hive_value_h)(value)

	ret, err := C.hivex_value_struct_length(h.han, n)
	if err != nil {
		return 0, err
	}
	return int64(ret), nil
}

// NodeValueDataCellOffset returns the length and the offset of the data cell
func (h *Hivex) NodeValueDataCellOffset(value int64) (length, offset int64, err error) {
	n := (C.hive_value_h)(value)

	var l C.size_t
	ret, err := C.hivex_value_data_cell_offset(h.han, n, &l)
	if err != nil {
		return
	}
	length = int64(l)
	offset = int64(ret)
	return
}

// ValueValue returns the raw value of the value address
func (h *Hivex) ValueValue(value int64) (valType int64, valueBytes []byte, err error) {
	n := (C.hive_value_h)(value)

	var t C.hive_type
	var l C.size_t

	ret, err := C.hivex_value_value(h.han, n, &t, &l)
	if err != nil {
		return
	}

	valueBytes = C.GoBytes(unsafe.Pointer(ret), C.int(l))
	valType = int64(t)
	return
}

// ValueString returns the value as a string (REG_SZ)
func (h *Hivex) ValueString(value int64) (string, error) {
	n := (C.hive_value_h)(value)

	ret, err := C.hivex_value_string(h.han, n)
	if err != nil {
		return "", err
	}

	return C.GoString(ret), nil
}

// ValueMultipleStrings returns a list of strings (REG_SZ_MULTI)
func (h *Hivex) ValueMultipleStrings(value int64) ([]string, error) {
	n := (C.hive_value_h)(value)
	ret, err := C.hivex_value_multiple_strings(h.han, n)
	if err != nil {
		return []string{}, err
	}

	nn := C.stringMembersSize(ret)
	if nn == 0 {
		return []string{}, nil
	}

	dest := make([]string, nn)
	var i C.size_t
	for i = 0; i < nn; i++ {
		val := C.memberValue(ret, i)
		// free the memory allocated to val
		defer C.free(unsafe.Pointer(val))
		dest[i] = C.GoString(val)
	}
	return dest, nil
}

// NodeValueDword returns the DWORD value
func (h *Hivex) NodeValueDword(value int64) (int32, error) {
	n := (C.hive_value_h)(value)

	ret, err := C.hivex_value_dword(h.han, n)
	if err != nil {
		return 0, err
	}
	return int32(ret), nil
}

// NodeValueQword returns the QWORD value
func (h *Hivex) NodeValueQword(value int64) (int64, error) {
	n := (C.hive_value_h)(value)

	ret, err := C.hivex_value_qword(h.han, n)
	if err != nil {
		return 0, err
	}
	return int64(ret), nil
}

// NodeAddChild adds a new node child
func (h *Hivex) NodeAddChild(parent int64, name string) (int64, error) {
	p := (C.hive_node_h)(parent)
	n := C.CString(name)
	defer C.free(unsafe.Pointer(n))
	ret, err := C.hivex_node_add_child(h.han, p, n)
	if err != nil {
		return 0, err
	}
	return int64(ret), nil
}

// NodeDeleteChild deletes a child node
func (h *Hivex) NodeDeleteChild(node int64) (int, error) {
	n := (C.hive_node_h)(node)
	ret, err := C.hivex_node_delete_child(h.han, n)
	if err != nil {
		return 0, err
	}
	return int(ret), nil
}

// HiveValue holds a new value that can be passed to hivex_node_set_value
type HiveValue struct {
	Type  int
	Key   string
	Value []byte
}

// NodeSetValues sets values on a node
func (h *Hivex) NodeSetValues(node int64, values []HiveValue) (int, error) {
	n := (C.hive_node_h)(node)
	var sz C.size_t = (C.size_t)(len(values))
	var val C.hive_set_value
	var vals *C.hive_set_value = (*C.hive_set_value)(
		C.malloc(
			C.size_t(
				int64(unsafe.Sizeof(val)) * int64(sz),
			),
		),
	)
	defer C.free(unsafe.Pointer(vals))

	for idx, val := range values {
		converted := C.hive_set_value{
			key:   C.CString(val.Key),
			t:     (C.hive_type)(val.Type),
			len:   (C.size_t)(len(val.Value)),
			value: (*C.char)(unsafe.Pointer(&val.Value[0])),
		}
		ret := C.set_values(vals, (C.int)(idx), (C.int)(sz), converted)
		if int(ret) != 0 {
			return 0, fmt.Errorf("failed to set value for nodes")
		}
	}
	ret, err := C.hivex_node_set_values(h.han, n, sz, vals, 0)
	if err != nil {
		return 0, err
	}
	return int(ret), nil
}

// NodeSetValue sets the value on one node
func (h *Hivex) NodeSetValue(node int64, value HiveValue) (int, error) {
	n := (C.hive_node_h)(node)
	var val C.hive_set_value
	var newVal *C.hive_set_value = (*C.hive_set_value)(
		C.malloc(
			C.size_t(unsafe.Sizeof(val)),
		),
	)
	defer C.free(unsafe.Pointer(newVal))

	converted := C.hive_set_value{
		key:   C.CString(value.Key),
		t:     (C.hive_type)(value.Type),
		len:   (C.size_t)(len(value.Value)),
		value: (*C.char)(unsafe.Pointer(&value.Value[0])),
	}
	setValRet := C.set_values(newVal, C.int(0), C.int(1), converted)
	if int(setValRet) != 0 {
		return 0, fmt.Errorf("failed to set value for nodes")
	}
	ret, err := C.hivex_node_set_value(h.han, n, newVal, 0)
	if err != nil {
		return 0, err
	}
	return int(ret), nil
}

// Commit commits all changes to the reg binary
func (h *Hivex) Commit() (int, error) {
	ret, err := C.hivex_commit(h.han, h.file, 0)
	if err != nil {
		return 0, err
	}
	return int(ret), nil
}
