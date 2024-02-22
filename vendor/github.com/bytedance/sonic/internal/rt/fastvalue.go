/*
 * Copyright 2021 ByteDance Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package rt

import (
    `reflect`
    `unsafe`
)

var (
    reflectRtypeItab = findReflectRtypeItab()
)

// GoType.KindFlags const
const (
    F_direct    = 1 << 5
    F_kind_mask = (1 << 5) - 1
)

// GoType.Flags const
const (
    tflagUncommon      uint8 = 1 << 0
    tflagExtraStar     uint8 = 1 << 1
    tflagNamed         uint8 = 1 << 2
    tflagRegularMemory uint8 = 1 << 3
)

type GoType struct {
    Size       uintptr
    PtrData    uintptr
    Hash       uint32
    Flags      uint8
    Align      uint8
    FieldAlign uint8
    KindFlags  uint8
    Traits     unsafe.Pointer
    GCData     *byte
    Str        int32
    PtrToSelf  int32
}

func (self *GoType) IsNamed() bool {
    return (self.Flags & tflagNamed) != 0
}

func (self *GoType) Kind() reflect.Kind {
    return reflect.Kind(self.KindFlags & F_kind_mask)
}

func (self *GoType) Pack() (t reflect.Type) {
    (*GoIface)(unsafe.Pointer(&t)).Itab = reflectRtypeItab
    (*GoIface)(unsafe.Pointer(&t)).Value = unsafe.Pointer(self)
    return
}

func (self *GoType) String() string {
    return self.Pack().String()
}

func (self *GoType) Indirect() bool {
    return self.KindFlags & F_direct == 0
}

type GoMap struct {
    Count      int
    Flags      uint8
    B          uint8
    Overflow   uint16
    Hash0      uint32
    Buckets    unsafe.Pointer
    OldBuckets unsafe.Pointer
    Evacuate   uintptr
    Extra      unsafe.Pointer
}

type GoMapIterator struct {
    K           unsafe.Pointer
    V           unsafe.Pointer
    T           *GoMapType
    H           *GoMap
    Buckets     unsafe.Pointer
    Bptr        *unsafe.Pointer
    Overflow    *[]unsafe.Pointer
    OldOverflow *[]unsafe.Pointer
    StartBucket uintptr
    Offset      uint8
    Wrapped     bool
    B           uint8
    I           uint8
    Bucket      uintptr
    CheckBucket uintptr
}

type GoItab struct {
    it unsafe.Pointer
    Vt *GoType
    hv uint32
    _  [4]byte
    fn [1]uintptr
}

type GoIface struct {
    Itab  *GoItab
    Value unsafe.Pointer
}

type GoEface struct {
    Type  *GoType
    Value unsafe.Pointer
}

func (self GoEface) Pack() (v interface{}) {
    *(*GoEface)(unsafe.Pointer(&v)) = self
    return
}

type GoPtrType struct {
    GoType
    Elem *GoType
}

type GoMapType struct {
    GoType
    Key        *GoType
    Elem       *GoType
    Bucket     *GoType
    Hasher     func(unsafe.Pointer, uintptr) uintptr
    KeySize    uint8
    ElemSize   uint8
    BucketSize uint16
    Flags      uint32
}

func (self *GoMapType) IndirectElem() bool {
    return self.Flags & 2 != 0
}

type GoStructType struct {
    GoType
    Pkg    *byte
    Fields []GoStructField
}

type GoStructField struct {
    Name     *byte
    Type     *GoType
    OffEmbed uintptr
}

type GoInterfaceType struct {
    GoType
    PkgPath *byte
    Methods []GoInterfaceMethod
}

type GoInterfaceMethod struct {
    Name int32
    Type int32
}

type GoSlice struct {
    Ptr unsafe.Pointer
    Len int
    Cap int
}

type GoString struct {
    Ptr unsafe.Pointer
    Len int
}

func PtrElem(t *GoType) *GoType {
    return (*GoPtrType)(unsafe.Pointer(t)).Elem
}

func MapType(t *GoType) *GoMapType {
    return (*GoMapType)(unsafe.Pointer(t))
}

func IfaceType(t *GoType) *GoInterfaceType {
    return (*GoInterfaceType)(unsafe.Pointer(t))
}

func UnpackType(t reflect.Type) *GoType {
    return (*GoType)((*GoIface)(unsafe.Pointer(&t)).Value)
}

func UnpackEface(v interface{}) GoEface {
    return *(*GoEface)(unsafe.Pointer(&v))
}

func UnpackIface(v interface{}) GoIface {
    return *(*GoIface)(unsafe.Pointer(&v))
}

func findReflectRtypeItab() *GoItab {
    v := reflect.TypeOf(struct{}{})
    return (*GoIface)(unsafe.Pointer(&v)).Itab
}
