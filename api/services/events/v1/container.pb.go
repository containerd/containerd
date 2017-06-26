// Code generated by protoc-gen-gogo.
// source: github.com/containerd/containerd/api/services/events/v1/container.proto
// DO NOT EDIT!

/*
	Package events is a generated protocol buffer package.

	It is generated from these files:
		github.com/containerd/containerd/api/services/events/v1/container.proto
		github.com/containerd/containerd/api/services/events/v1/content.proto
		github.com/containerd/containerd/api/services/events/v1/events.proto
		github.com/containerd/containerd/api/services/events/v1/image.proto
		github.com/containerd/containerd/api/services/events/v1/namespace.proto
		github.com/containerd/containerd/api/services/events/v1/runtime.proto
		github.com/containerd/containerd/api/services/events/v1/snapshot.proto
		github.com/containerd/containerd/api/services/events/v1/task.proto

	It has these top-level messages:
		ContainerCreate
		ContainerUpdate
		ContainerDelete
		ContentDelete
		Envelope
		StreamEventsRequest
		ImageUpdate
		ImageDelete
		NamespaceCreate
		NamespaceUpdate
		NamespaceDelete
		RuntimeIO
		RuntimeMount
		RuntimeCreate
		RuntimeEvent
		RuntimeDelete
		SnapshotPrepare
		SnapshotCommit
		SnapshotRemove
		TaskCreate
		TaskStart
		TaskDelete
*/
package events

import proto "github.com/gogo/protobuf/proto"
import fmt "fmt"
import math "math"
import _ "github.com/gogo/protobuf/gogoproto"

import strings "strings"
import reflect "reflect"
import github_com_gogo_protobuf_sortkeys "github.com/gogo/protobuf/sortkeys"

import io "io"

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.GoGoProtoPackageIsVersion2 // please upgrade the proto package

type ContainerCreate struct {
	ContainerID string                   `protobuf:"bytes,1,opt,name=container_id,json=containerId,proto3" json:"container_id,omitempty"`
	Image       string                   `protobuf:"bytes,2,opt,name=image,proto3" json:"image,omitempty"`
	Runtime     *ContainerCreate_Runtime `protobuf:"bytes,3,opt,name=runtime" json:"runtime,omitempty"`
}

func (m *ContainerCreate) Reset()                    { *m = ContainerCreate{} }
func (*ContainerCreate) ProtoMessage()               {}
func (*ContainerCreate) Descriptor() ([]byte, []int) { return fileDescriptorContainer, []int{0} }

type ContainerCreate_Runtime struct {
	Name    string            `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Options map[string]string `protobuf:"bytes,2,rep,name=options" json:"options,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
}

func (m *ContainerCreate_Runtime) Reset()      { *m = ContainerCreate_Runtime{} }
func (*ContainerCreate_Runtime) ProtoMessage() {}
func (*ContainerCreate_Runtime) Descriptor() ([]byte, []int) {
	return fileDescriptorContainer, []int{0, 0}
}

type ContainerUpdate struct {
	ContainerID string            `protobuf:"bytes,1,opt,name=container_id,json=containerId,proto3" json:"container_id,omitempty"`
	Image       string            `protobuf:"bytes,2,opt,name=image,proto3" json:"image,omitempty"`
	Labels      map[string]string `protobuf:"bytes,3,rep,name=labels" json:"labels,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
	RootFS      string            `protobuf:"bytes,4,opt,name=rootfs,proto3" json:"rootfs,omitempty"`
}

func (m *ContainerUpdate) Reset()                    { *m = ContainerUpdate{} }
func (*ContainerUpdate) ProtoMessage()               {}
func (*ContainerUpdate) Descriptor() ([]byte, []int) { return fileDescriptorContainer, []int{1} }

type ContainerDelete struct {
	ContainerID string `protobuf:"bytes,1,opt,name=container_id,json=containerId,proto3" json:"container_id,omitempty"`
	Image       string `protobuf:"bytes,2,opt,name=image,proto3" json:"image,omitempty"`
}

func (m *ContainerDelete) Reset()                    { *m = ContainerDelete{} }
func (*ContainerDelete) ProtoMessage()               {}
func (*ContainerDelete) Descriptor() ([]byte, []int) { return fileDescriptorContainer, []int{2} }

func init() {
	proto.RegisterType((*ContainerCreate)(nil), "containerd.services.events.v1.ContainerCreate")
	proto.RegisterType((*ContainerCreate_Runtime)(nil), "containerd.services.events.v1.ContainerCreate.Runtime")
	proto.RegisterType((*ContainerUpdate)(nil), "containerd.services.events.v1.ContainerUpdate")
	proto.RegisterType((*ContainerDelete)(nil), "containerd.services.events.v1.ContainerDelete")
}
func (m *ContainerCreate) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalTo(dAtA)
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *ContainerCreate) MarshalTo(dAtA []byte) (int, error) {
	var i int
	_ = i
	var l int
	_ = l
	if len(m.ContainerID) > 0 {
		dAtA[i] = 0xa
		i++
		i = encodeVarintContainer(dAtA, i, uint64(len(m.ContainerID)))
		i += copy(dAtA[i:], m.ContainerID)
	}
	if len(m.Image) > 0 {
		dAtA[i] = 0x12
		i++
		i = encodeVarintContainer(dAtA, i, uint64(len(m.Image)))
		i += copy(dAtA[i:], m.Image)
	}
	if m.Runtime != nil {
		dAtA[i] = 0x1a
		i++
		i = encodeVarintContainer(dAtA, i, uint64(m.Runtime.Size()))
		n1, err := m.Runtime.MarshalTo(dAtA[i:])
		if err != nil {
			return 0, err
		}
		i += n1
	}
	return i, nil
}

func (m *ContainerCreate_Runtime) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalTo(dAtA)
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *ContainerCreate_Runtime) MarshalTo(dAtA []byte) (int, error) {
	var i int
	_ = i
	var l int
	_ = l
	if len(m.Name) > 0 {
		dAtA[i] = 0xa
		i++
		i = encodeVarintContainer(dAtA, i, uint64(len(m.Name)))
		i += copy(dAtA[i:], m.Name)
	}
	if len(m.Options) > 0 {
		for k, _ := range m.Options {
			dAtA[i] = 0x12
			i++
			v := m.Options[k]
			mapSize := 1 + len(k) + sovContainer(uint64(len(k))) + 1 + len(v) + sovContainer(uint64(len(v)))
			i = encodeVarintContainer(dAtA, i, uint64(mapSize))
			dAtA[i] = 0xa
			i++
			i = encodeVarintContainer(dAtA, i, uint64(len(k)))
			i += copy(dAtA[i:], k)
			dAtA[i] = 0x12
			i++
			i = encodeVarintContainer(dAtA, i, uint64(len(v)))
			i += copy(dAtA[i:], v)
		}
	}
	return i, nil
}

func (m *ContainerUpdate) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalTo(dAtA)
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *ContainerUpdate) MarshalTo(dAtA []byte) (int, error) {
	var i int
	_ = i
	var l int
	_ = l
	if len(m.ContainerID) > 0 {
		dAtA[i] = 0xa
		i++
		i = encodeVarintContainer(dAtA, i, uint64(len(m.ContainerID)))
		i += copy(dAtA[i:], m.ContainerID)
	}
	if len(m.Image) > 0 {
		dAtA[i] = 0x12
		i++
		i = encodeVarintContainer(dAtA, i, uint64(len(m.Image)))
		i += copy(dAtA[i:], m.Image)
	}
	if len(m.Labels) > 0 {
		for k, _ := range m.Labels {
			dAtA[i] = 0x1a
			i++
			v := m.Labels[k]
			mapSize := 1 + len(k) + sovContainer(uint64(len(k))) + 1 + len(v) + sovContainer(uint64(len(v)))
			i = encodeVarintContainer(dAtA, i, uint64(mapSize))
			dAtA[i] = 0xa
			i++
			i = encodeVarintContainer(dAtA, i, uint64(len(k)))
			i += copy(dAtA[i:], k)
			dAtA[i] = 0x12
			i++
			i = encodeVarintContainer(dAtA, i, uint64(len(v)))
			i += copy(dAtA[i:], v)
		}
	}
	if len(m.RootFS) > 0 {
		dAtA[i] = 0x22
		i++
		i = encodeVarintContainer(dAtA, i, uint64(len(m.RootFS)))
		i += copy(dAtA[i:], m.RootFS)
	}
	return i, nil
}

func (m *ContainerDelete) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalTo(dAtA)
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *ContainerDelete) MarshalTo(dAtA []byte) (int, error) {
	var i int
	_ = i
	var l int
	_ = l
	if len(m.ContainerID) > 0 {
		dAtA[i] = 0xa
		i++
		i = encodeVarintContainer(dAtA, i, uint64(len(m.ContainerID)))
		i += copy(dAtA[i:], m.ContainerID)
	}
	if len(m.Image) > 0 {
		dAtA[i] = 0x12
		i++
		i = encodeVarintContainer(dAtA, i, uint64(len(m.Image)))
		i += copy(dAtA[i:], m.Image)
	}
	return i, nil
}

func encodeFixed64Container(dAtA []byte, offset int, v uint64) int {
	dAtA[offset] = uint8(v)
	dAtA[offset+1] = uint8(v >> 8)
	dAtA[offset+2] = uint8(v >> 16)
	dAtA[offset+3] = uint8(v >> 24)
	dAtA[offset+4] = uint8(v >> 32)
	dAtA[offset+5] = uint8(v >> 40)
	dAtA[offset+6] = uint8(v >> 48)
	dAtA[offset+7] = uint8(v >> 56)
	return offset + 8
}
func encodeFixed32Container(dAtA []byte, offset int, v uint32) int {
	dAtA[offset] = uint8(v)
	dAtA[offset+1] = uint8(v >> 8)
	dAtA[offset+2] = uint8(v >> 16)
	dAtA[offset+3] = uint8(v >> 24)
	return offset + 4
}
func encodeVarintContainer(dAtA []byte, offset int, v uint64) int {
	for v >= 1<<7 {
		dAtA[offset] = uint8(v&0x7f | 0x80)
		v >>= 7
		offset++
	}
	dAtA[offset] = uint8(v)
	return offset + 1
}
func (m *ContainerCreate) Size() (n int) {
	var l int
	_ = l
	l = len(m.ContainerID)
	if l > 0 {
		n += 1 + l + sovContainer(uint64(l))
	}
	l = len(m.Image)
	if l > 0 {
		n += 1 + l + sovContainer(uint64(l))
	}
	if m.Runtime != nil {
		l = m.Runtime.Size()
		n += 1 + l + sovContainer(uint64(l))
	}
	return n
}

func (m *ContainerCreate_Runtime) Size() (n int) {
	var l int
	_ = l
	l = len(m.Name)
	if l > 0 {
		n += 1 + l + sovContainer(uint64(l))
	}
	if len(m.Options) > 0 {
		for k, v := range m.Options {
			_ = k
			_ = v
			mapEntrySize := 1 + len(k) + sovContainer(uint64(len(k))) + 1 + len(v) + sovContainer(uint64(len(v)))
			n += mapEntrySize + 1 + sovContainer(uint64(mapEntrySize))
		}
	}
	return n
}

func (m *ContainerUpdate) Size() (n int) {
	var l int
	_ = l
	l = len(m.ContainerID)
	if l > 0 {
		n += 1 + l + sovContainer(uint64(l))
	}
	l = len(m.Image)
	if l > 0 {
		n += 1 + l + sovContainer(uint64(l))
	}
	if len(m.Labels) > 0 {
		for k, v := range m.Labels {
			_ = k
			_ = v
			mapEntrySize := 1 + len(k) + sovContainer(uint64(len(k))) + 1 + len(v) + sovContainer(uint64(len(v)))
			n += mapEntrySize + 1 + sovContainer(uint64(mapEntrySize))
		}
	}
	l = len(m.RootFS)
	if l > 0 {
		n += 1 + l + sovContainer(uint64(l))
	}
	return n
}

func (m *ContainerDelete) Size() (n int) {
	var l int
	_ = l
	l = len(m.ContainerID)
	if l > 0 {
		n += 1 + l + sovContainer(uint64(l))
	}
	l = len(m.Image)
	if l > 0 {
		n += 1 + l + sovContainer(uint64(l))
	}
	return n
}

func sovContainer(x uint64) (n int) {
	for {
		n++
		x >>= 7
		if x == 0 {
			break
		}
	}
	return n
}
func sozContainer(x uint64) (n int) {
	return sovContainer(uint64((x << 1) ^ uint64((int64(x) >> 63))))
}
func (this *ContainerCreate) String() string {
	if this == nil {
		return "nil"
	}
	s := strings.Join([]string{`&ContainerCreate{`,
		`ContainerID:` + fmt.Sprintf("%v", this.ContainerID) + `,`,
		`Image:` + fmt.Sprintf("%v", this.Image) + `,`,
		`Runtime:` + strings.Replace(fmt.Sprintf("%v", this.Runtime), "ContainerCreate_Runtime", "ContainerCreate_Runtime", 1) + `,`,
		`}`,
	}, "")
	return s
}
func (this *ContainerCreate_Runtime) String() string {
	if this == nil {
		return "nil"
	}
	keysForOptions := make([]string, 0, len(this.Options))
	for k, _ := range this.Options {
		keysForOptions = append(keysForOptions, k)
	}
	github_com_gogo_protobuf_sortkeys.Strings(keysForOptions)
	mapStringForOptions := "map[string]string{"
	for _, k := range keysForOptions {
		mapStringForOptions += fmt.Sprintf("%v: %v,", k, this.Options[k])
	}
	mapStringForOptions += "}"
	s := strings.Join([]string{`&ContainerCreate_Runtime{`,
		`Name:` + fmt.Sprintf("%v", this.Name) + `,`,
		`Options:` + mapStringForOptions + `,`,
		`}`,
	}, "")
	return s
}
func (this *ContainerUpdate) String() string {
	if this == nil {
		return "nil"
	}
	keysForLabels := make([]string, 0, len(this.Labels))
	for k, _ := range this.Labels {
		keysForLabels = append(keysForLabels, k)
	}
	github_com_gogo_protobuf_sortkeys.Strings(keysForLabels)
	mapStringForLabels := "map[string]string{"
	for _, k := range keysForLabels {
		mapStringForLabels += fmt.Sprintf("%v: %v,", k, this.Labels[k])
	}
	mapStringForLabels += "}"
	s := strings.Join([]string{`&ContainerUpdate{`,
		`ContainerID:` + fmt.Sprintf("%v", this.ContainerID) + `,`,
		`Image:` + fmt.Sprintf("%v", this.Image) + `,`,
		`Labels:` + mapStringForLabels + `,`,
		`RootFS:` + fmt.Sprintf("%v", this.RootFS) + `,`,
		`}`,
	}, "")
	return s
}
func (this *ContainerDelete) String() string {
	if this == nil {
		return "nil"
	}
	s := strings.Join([]string{`&ContainerDelete{`,
		`ContainerID:` + fmt.Sprintf("%v", this.ContainerID) + `,`,
		`Image:` + fmt.Sprintf("%v", this.Image) + `,`,
		`}`,
	}, "")
	return s
}
func valueToStringContainer(v interface{}) string {
	rv := reflect.ValueOf(v)
	if rv.IsNil() {
		return "nil"
	}
	pv := reflect.Indirect(rv).Interface()
	return fmt.Sprintf("*%v", pv)
}
func (m *ContainerCreate) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowContainer
			}
			if iNdEx >= l {
				return io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= (uint64(b) & 0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		fieldNum := int32(wire >> 3)
		wireType := int(wire & 0x7)
		if wireType == 4 {
			return fmt.Errorf("proto: ContainerCreate: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: ContainerCreate: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 1:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field ContainerID", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowContainer
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= (uint64(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthContainer
			}
			postIndex := iNdEx + intStringLen
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.ContainerID = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 2:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Image", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowContainer
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= (uint64(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthContainer
			}
			postIndex := iNdEx + intStringLen
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Image = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 3:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Runtime", wireType)
			}
			var msglen int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowContainer
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				msglen |= (int(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if msglen < 0 {
				return ErrInvalidLengthContainer
			}
			postIndex := iNdEx + msglen
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			if m.Runtime == nil {
				m.Runtime = &ContainerCreate_Runtime{}
			}
			if err := m.Runtime.Unmarshal(dAtA[iNdEx:postIndex]); err != nil {
				return err
			}
			iNdEx = postIndex
		default:
			iNdEx = preIndex
			skippy, err := skipContainer(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if skippy < 0 {
				return ErrInvalidLengthContainer
			}
			if (iNdEx + skippy) > l {
				return io.ErrUnexpectedEOF
			}
			iNdEx += skippy
		}
	}

	if iNdEx > l {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func (m *ContainerCreate_Runtime) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowContainer
			}
			if iNdEx >= l {
				return io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= (uint64(b) & 0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		fieldNum := int32(wire >> 3)
		wireType := int(wire & 0x7)
		if wireType == 4 {
			return fmt.Errorf("proto: Runtime: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: Runtime: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 1:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Name", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowContainer
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= (uint64(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthContainer
			}
			postIndex := iNdEx + intStringLen
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Name = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 2:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Options", wireType)
			}
			var msglen int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowContainer
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				msglen |= (int(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if msglen < 0 {
				return ErrInvalidLengthContainer
			}
			postIndex := iNdEx + msglen
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			var keykey uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowContainer
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				keykey |= (uint64(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			var stringLenmapkey uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowContainer
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLenmapkey |= (uint64(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLenmapkey := int(stringLenmapkey)
			if intStringLenmapkey < 0 {
				return ErrInvalidLengthContainer
			}
			postStringIndexmapkey := iNdEx + intStringLenmapkey
			if postStringIndexmapkey > l {
				return io.ErrUnexpectedEOF
			}
			mapkey := string(dAtA[iNdEx:postStringIndexmapkey])
			iNdEx = postStringIndexmapkey
			if m.Options == nil {
				m.Options = make(map[string]string)
			}
			if iNdEx < postIndex {
				var valuekey uint64
				for shift := uint(0); ; shift += 7 {
					if shift >= 64 {
						return ErrIntOverflowContainer
					}
					if iNdEx >= l {
						return io.ErrUnexpectedEOF
					}
					b := dAtA[iNdEx]
					iNdEx++
					valuekey |= (uint64(b) & 0x7F) << shift
					if b < 0x80 {
						break
					}
				}
				var stringLenmapvalue uint64
				for shift := uint(0); ; shift += 7 {
					if shift >= 64 {
						return ErrIntOverflowContainer
					}
					if iNdEx >= l {
						return io.ErrUnexpectedEOF
					}
					b := dAtA[iNdEx]
					iNdEx++
					stringLenmapvalue |= (uint64(b) & 0x7F) << shift
					if b < 0x80 {
						break
					}
				}
				intStringLenmapvalue := int(stringLenmapvalue)
				if intStringLenmapvalue < 0 {
					return ErrInvalidLengthContainer
				}
				postStringIndexmapvalue := iNdEx + intStringLenmapvalue
				if postStringIndexmapvalue > l {
					return io.ErrUnexpectedEOF
				}
				mapvalue := string(dAtA[iNdEx:postStringIndexmapvalue])
				iNdEx = postStringIndexmapvalue
				m.Options[mapkey] = mapvalue
			} else {
				var mapvalue string
				m.Options[mapkey] = mapvalue
			}
			iNdEx = postIndex
		default:
			iNdEx = preIndex
			skippy, err := skipContainer(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if skippy < 0 {
				return ErrInvalidLengthContainer
			}
			if (iNdEx + skippy) > l {
				return io.ErrUnexpectedEOF
			}
			iNdEx += skippy
		}
	}

	if iNdEx > l {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func (m *ContainerUpdate) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowContainer
			}
			if iNdEx >= l {
				return io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= (uint64(b) & 0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		fieldNum := int32(wire >> 3)
		wireType := int(wire & 0x7)
		if wireType == 4 {
			return fmt.Errorf("proto: ContainerUpdate: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: ContainerUpdate: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 1:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field ContainerID", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowContainer
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= (uint64(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthContainer
			}
			postIndex := iNdEx + intStringLen
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.ContainerID = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 2:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Image", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowContainer
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= (uint64(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthContainer
			}
			postIndex := iNdEx + intStringLen
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Image = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 3:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Labels", wireType)
			}
			var msglen int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowContainer
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				msglen |= (int(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if msglen < 0 {
				return ErrInvalidLengthContainer
			}
			postIndex := iNdEx + msglen
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			var keykey uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowContainer
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				keykey |= (uint64(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			var stringLenmapkey uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowContainer
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLenmapkey |= (uint64(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLenmapkey := int(stringLenmapkey)
			if intStringLenmapkey < 0 {
				return ErrInvalidLengthContainer
			}
			postStringIndexmapkey := iNdEx + intStringLenmapkey
			if postStringIndexmapkey > l {
				return io.ErrUnexpectedEOF
			}
			mapkey := string(dAtA[iNdEx:postStringIndexmapkey])
			iNdEx = postStringIndexmapkey
			if m.Labels == nil {
				m.Labels = make(map[string]string)
			}
			if iNdEx < postIndex {
				var valuekey uint64
				for shift := uint(0); ; shift += 7 {
					if shift >= 64 {
						return ErrIntOverflowContainer
					}
					if iNdEx >= l {
						return io.ErrUnexpectedEOF
					}
					b := dAtA[iNdEx]
					iNdEx++
					valuekey |= (uint64(b) & 0x7F) << shift
					if b < 0x80 {
						break
					}
				}
				var stringLenmapvalue uint64
				for shift := uint(0); ; shift += 7 {
					if shift >= 64 {
						return ErrIntOverflowContainer
					}
					if iNdEx >= l {
						return io.ErrUnexpectedEOF
					}
					b := dAtA[iNdEx]
					iNdEx++
					stringLenmapvalue |= (uint64(b) & 0x7F) << shift
					if b < 0x80 {
						break
					}
				}
				intStringLenmapvalue := int(stringLenmapvalue)
				if intStringLenmapvalue < 0 {
					return ErrInvalidLengthContainer
				}
				postStringIndexmapvalue := iNdEx + intStringLenmapvalue
				if postStringIndexmapvalue > l {
					return io.ErrUnexpectedEOF
				}
				mapvalue := string(dAtA[iNdEx:postStringIndexmapvalue])
				iNdEx = postStringIndexmapvalue
				m.Labels[mapkey] = mapvalue
			} else {
				var mapvalue string
				m.Labels[mapkey] = mapvalue
			}
			iNdEx = postIndex
		case 4:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field RootFS", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowContainer
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= (uint64(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthContainer
			}
			postIndex := iNdEx + intStringLen
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.RootFS = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		default:
			iNdEx = preIndex
			skippy, err := skipContainer(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if skippy < 0 {
				return ErrInvalidLengthContainer
			}
			if (iNdEx + skippy) > l {
				return io.ErrUnexpectedEOF
			}
			iNdEx += skippy
		}
	}

	if iNdEx > l {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func (m *ContainerDelete) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowContainer
			}
			if iNdEx >= l {
				return io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= (uint64(b) & 0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		fieldNum := int32(wire >> 3)
		wireType := int(wire & 0x7)
		if wireType == 4 {
			return fmt.Errorf("proto: ContainerDelete: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: ContainerDelete: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 1:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field ContainerID", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowContainer
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= (uint64(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthContainer
			}
			postIndex := iNdEx + intStringLen
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.ContainerID = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 2:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Image", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowContainer
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= (uint64(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthContainer
			}
			postIndex := iNdEx + intStringLen
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Image = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		default:
			iNdEx = preIndex
			skippy, err := skipContainer(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if skippy < 0 {
				return ErrInvalidLengthContainer
			}
			if (iNdEx + skippy) > l {
				return io.ErrUnexpectedEOF
			}
			iNdEx += skippy
		}
	}

	if iNdEx > l {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func skipContainer(dAtA []byte) (n int, err error) {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return 0, ErrIntOverflowContainer
			}
			if iNdEx >= l {
				return 0, io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= (uint64(b) & 0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		wireType := int(wire & 0x7)
		switch wireType {
		case 0:
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return 0, ErrIntOverflowContainer
				}
				if iNdEx >= l {
					return 0, io.ErrUnexpectedEOF
				}
				iNdEx++
				if dAtA[iNdEx-1] < 0x80 {
					break
				}
			}
			return iNdEx, nil
		case 1:
			iNdEx += 8
			return iNdEx, nil
		case 2:
			var length int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return 0, ErrIntOverflowContainer
				}
				if iNdEx >= l {
					return 0, io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				length |= (int(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			iNdEx += length
			if length < 0 {
				return 0, ErrInvalidLengthContainer
			}
			return iNdEx, nil
		case 3:
			for {
				var innerWire uint64
				var start int = iNdEx
				for shift := uint(0); ; shift += 7 {
					if shift >= 64 {
						return 0, ErrIntOverflowContainer
					}
					if iNdEx >= l {
						return 0, io.ErrUnexpectedEOF
					}
					b := dAtA[iNdEx]
					iNdEx++
					innerWire |= (uint64(b) & 0x7F) << shift
					if b < 0x80 {
						break
					}
				}
				innerWireType := int(innerWire & 0x7)
				if innerWireType == 4 {
					break
				}
				next, err := skipContainer(dAtA[start:])
				if err != nil {
					return 0, err
				}
				iNdEx = start + next
			}
			return iNdEx, nil
		case 4:
			return iNdEx, nil
		case 5:
			iNdEx += 4
			return iNdEx, nil
		default:
			return 0, fmt.Errorf("proto: illegal wireType %d", wireType)
		}
	}
	panic("unreachable")
}

var (
	ErrInvalidLengthContainer = fmt.Errorf("proto: negative length found during unmarshaling")
	ErrIntOverflowContainer   = fmt.Errorf("proto: integer overflow")
)

func init() {
	proto.RegisterFile("github.com/containerd/containerd/api/services/events/v1/container.proto", fileDescriptorContainer)
}

var fileDescriptorContainer = []byte{
	// 406 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0xac, 0x53, 0xc1, 0xaa, 0xd3, 0x40,
	0x14, 0xed, 0x24, 0x35, 0xc5, 0x49, 0xa1, 0x32, 0x74, 0x11, 0x02, 0xa6, 0xa5, 0xab, 0xae, 0x26,
	0xb4, 0x82, 0x68, 0x05, 0x17, 0x6d, 0x55, 0x0a, 0x82, 0x32, 0x22, 0x88, 0x22, 0x92, 0x36, 0x63,
	0x1c, 0x4c, 0x32, 0x21, 0x99, 0x06, 0xba, 0xf3, 0x0b, 0xfc, 0x1e, 0x3f, 0xa1, 0x4b, 0x97, 0xae,
	0x8a, 0xcd, 0xea, 0x7d, 0xc6, 0x23, 0x99, 0x24, 0x0d, 0x6f, 0xf1, 0x78, 0xaf, 0x74, 0x77, 0x26,
	0xf7, 0x9c, 0x93, 0x73, 0x0f, 0x5c, 0xf8, 0xc6, 0x63, 0xe2, 0xc7, 0x76, 0x8d, 0x37, 0x3c, 0xb0,
	0x37, 0x3c, 0x14, 0x0e, 0x0b, 0x69, 0xec, 0x36, 0xa1, 0x13, 0x31, 0x3b, 0xa1, 0x71, 0xca, 0x36,
	0x34, 0xb1, 0x69, 0x4a, 0x43, 0x91, 0xd8, 0xe9, 0xe4, 0xc4, 0xc0, 0x51, 0xcc, 0x05, 0x47, 0x8f,
	0x4f, 0x12, 0x5c, 0xd1, 0xb1, 0xa4, 0xe3, 0x74, 0x62, 0xf6, 0x3d, 0xee, 0xf1, 0x82, 0x69, 0xe7,
	0x48, 0x8a, 0x46, 0x57, 0x0a, 0xec, 0x2d, 0x2a, 0xdd, 0x22, 0xa6, 0x8e, 0xa0, 0x68, 0x0a, 0xbb,
	0xb5, 0xd5, 0x37, 0xe6, 0x1a, 0x60, 0x08, 0xc6, 0x0f, 0xe7, 0xbd, 0xec, 0x30, 0xd0, 0x6b, 0xea,
	0x6a, 0x49, 0xf4, 0x9a, 0xb4, 0x72, 0x51, 0x1f, 0x3e, 0x60, 0x81, 0xe3, 0x51, 0x43, 0xc9, 0xc9,
	0x44, 0x3e, 0xd0, 0x7b, 0xd8, 0x89, 0xb7, 0xa1, 0x60, 0x01, 0x35, 0xd4, 0x21, 0x18, 0xeb, 0xd3,
	0xa7, 0xf8, 0xd6, 0x90, 0xf8, 0x46, 0x14, 0x4c, 0xa4, 0x9a, 0x54, 0x36, 0xe6, 0x1f, 0x00, 0x3b,
	0xe5, 0x47, 0x84, 0x60, 0x3b, 0x74, 0x02, 0x2a, 0xf3, 0x91, 0x02, 0xa3, 0xaf, 0xb0, 0xc3, 0x23,
	0xc1, 0x78, 0x98, 0x18, 0xca, 0x50, 0x1d, 0xeb, 0xd3, 0xc5, 0x79, 0x7f, 0xc4, 0xef, 0xa4, 0xcb,
	0xab, 0x50, 0xc4, 0x3b, 0x52, 0x79, 0x9a, 0x33, 0xd8, 0x6d, 0x0e, 0xd0, 0x23, 0xa8, 0xfe, 0xa4,
	0xbb, 0x32, 0x41, 0x0e, 0xf3, 0x22, 0x52, 0xc7, 0xdf, 0xd6, 0x45, 0x14, 0x8f, 0x99, 0xf2, 0x0c,
	0x8c, 0x7e, 0x37, 0xab, 0xfe, 0x18, 0xb9, 0x97, 0xad, 0x9a, 0x40, 0xcd, 0x77, 0xd6, 0xd4, 0x4f,
	0x0c, 0xb5, 0xd8, 0x7b, 0x76, 0xd7, 0xbd, 0x65, 0x12, 0xfc, 0xb6, 0x10, 0xcb, 0x75, 0x4b, 0x27,
	0x34, 0x82, 0x5a, 0xcc, 0xb9, 0xf8, 0x9e, 0x18, 0xed, 0x22, 0x17, 0xcc, 0x0e, 0x03, 0x8d, 0x70,
	0x2e, 0x5e, 0x7f, 0x20, 0xe5, 0xc4, 0x7c, 0x0e, 0xf5, 0x86, 0xf4, 0x5e, 0x85, 0x7c, 0x69, 0xf4,
	0xb1, 0xa4, 0x3e, 0xbd, 0x64, 0x1f, 0xf3, 0x4f, 0xfb, 0xa3, 0xd5, 0xfa, 0x77, 0xb4, 0x5a, 0xbf,
	0x32, 0x0b, 0xec, 0x33, 0x0b, 0xfc, 0xcd, 0x2c, 0xf0, 0x3f, 0xb3, 0xc0, 0xe7, 0x97, 0x67, 0x1e,
	0xdc, 0x0b, 0x89, 0xd6, 0x5a, 0x71, 0x39, 0x4f, 0xae, 0x03, 0x00, 0x00, 0xff, 0xff, 0x39, 0x00,
	0x34, 0x8b, 0xb9, 0x03, 0x00, 0x00,
}
