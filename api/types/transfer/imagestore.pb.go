//
//Copyright The containerd Authors.
//
//Licensed under the Apache License, Version 2.0 (the "License");
//you may not use this file except in compliance with the License.
//You may obtain a copy of the License at
//
//http://www.apache.org/licenses/LICENSE-2.0
//
//Unless required by applicable law or agreed to in writing, software
//distributed under the License is distributed on an "AS IS" BASIS,
//WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//See the License for the specific language governing permissions and
//limitations under the License.

// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.28.1
// 	protoc        v3.20.1
// source: github.com/containerd/containerd/api/types/transfer/imagestore.proto

package transfer

import (
	types "github.com/containerd/containerd/api/types"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type ImageStore struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name          string            `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Labels        map[string]string `protobuf:"bytes,2,rep,name=labels,proto3" json:"labels,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
	Platforms     []*types.Platform `protobuf:"bytes,3,rep,name=platforms,proto3" json:"platforms,omitempty"`
	AllMetadata   bool              `protobuf:"varint,4,opt,name=all_metadata,json=allMetadata,proto3" json:"all_metadata,omitempty"`
	ManifestLimit uint32            `protobuf:"varint,5,opt,name=manifest_limit,json=manifestLimit,proto3" json:"manifest_limit,omitempty"`
	// extra_references are used to set image names on imports of sub-images from the index
	ExtraReferences []*ImageReference `protobuf:"bytes,6,rep,name=extra_references,json=extraReferences,proto3" json:"extra_references,omitempty"`
	// export_images is a list of names of images to export
	ExportImages []string `protobuf:"bytes,7,rep,name=export_images,json=exportImages,proto3" json:"export_images,omitempty"`
	// Unpack Configuration, multiple allowed
	Unpacks []*UnpackConfiguration `protobuf:"bytes,10,rep,name=unpacks,proto3" json:"unpacks,omitempty"`
}

func (x *ImageStore) Reset() {
	*x = ImageStore{}
	if protoimpl.UnsafeEnabled {
		mi := &file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ImageStore) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ImageStore) ProtoMessage() {}

func (x *ImageStore) ProtoReflect() protoreflect.Message {
	mi := &file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ImageStore.ProtoReflect.Descriptor instead.
func (*ImageStore) Descriptor() ([]byte, []int) {
	return file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_rawDescGZIP(), []int{0}
}

func (x *ImageStore) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *ImageStore) GetLabels() map[string]string {
	if x != nil {
		return x.Labels
	}
	return nil
}

func (x *ImageStore) GetPlatforms() []*types.Platform {
	if x != nil {
		return x.Platforms
	}
	return nil
}

func (x *ImageStore) GetAllMetadata() bool {
	if x != nil {
		return x.AllMetadata
	}
	return false
}

func (x *ImageStore) GetManifestLimit() uint32 {
	if x != nil {
		return x.ManifestLimit
	}
	return 0
}

func (x *ImageStore) GetExtraReferences() []*ImageReference {
	if x != nil {
		return x.ExtraReferences
	}
	return nil
}

func (x *ImageStore) GetExportImages() []string {
	if x != nil {
		return x.ExportImages
	}
	return nil
}

func (x *ImageStore) GetUnpacks() []*UnpackConfiguration {
	if x != nil {
		return x.Unpacks
	}
	return nil
}

type UnpackConfiguration struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// platform is the platform to unpack for, used for resolving manifest and snapshotter
	// if not provided
	Platform *types.Platform `protobuf:"bytes,1,opt,name=platform,proto3" json:"platform,omitempty"`
	// snapshotter to unpack to, if not provided default for platform shoudl be used
	Snapshotter string `protobuf:"bytes,2,opt,name=snapshotter,proto3" json:"snapshotter,omitempty"`
}

func (x *UnpackConfiguration) Reset() {
	*x = UnpackConfiguration{}
	if protoimpl.UnsafeEnabled {
		mi := &file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *UnpackConfiguration) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UnpackConfiguration) ProtoMessage() {}

func (x *UnpackConfiguration) ProtoReflect() protoreflect.Message {
	mi := &file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UnpackConfiguration.ProtoReflect.Descriptor instead.
func (*UnpackConfiguration) Descriptor() ([]byte, []int) {
	return file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_rawDescGZIP(), []int{1}
}

func (x *UnpackConfiguration) GetPlatform() *types.Platform {
	if x != nil {
		return x.Platform
	}
	return nil
}

func (x *UnpackConfiguration) GetSnapshotter() string {
	if x != nil {
		return x.Snapshotter
	}
	return ""
}

// ImageReference is used to create or find a reference for an image
type ImageReference struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	// is_prefix determines whether the Name should be considered
	// a prefix (without tag or digest).
	// For lookup, this may allow matching multiple tags.
	// For store, this must have a tag or digest added.
	IsPrefix bool `protobuf:"varint,2,opt,name=is_prefix,json=isPrefix,proto3" json:"is_prefix,omitempty"`
	// allow_overwrite allows overwriting or ignoring the name if
	// another reference is provided (such as through an annotation).
	// Only used if IsPrefix is true.
	AllowOverwrite bool `protobuf:"varint,3,opt,name=allow_overwrite,json=allowOverwrite,proto3" json:"allow_overwrite,omitempty"`
	// add_digest adds the manifest digest to the reference.
	// For lookup, this allows matching tags with any digest.
	// For store, this allows adding the digest to the name.
	// Only used if IsPrefix is true.
	AddDigest bool `protobuf:"varint,4,opt,name=add_digest,json=addDigest,proto3" json:"add_digest,omitempty"`
	// skip_named_digest only considers digest references which do not
	// have a non-digested named reference.
	// For lookup, this will deduplicate digest references when there is a named match.
	// For store, this only adds this digest reference when there is no matching full
	// name reference from the prefix.
	// Only used if IsPrefix is true.
	SkipNamedDigest bool `protobuf:"varint,5,opt,name=skip_named_digest,json=skipNamedDigest,proto3" json:"skip_named_digest,omitempty"`
}

func (x *ImageReference) Reset() {
	*x = ImageReference{}
	if protoimpl.UnsafeEnabled {
		mi := &file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ImageReference) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ImageReference) ProtoMessage() {}

func (x *ImageReference) ProtoReflect() protoreflect.Message {
	mi := &file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ImageReference.ProtoReflect.Descriptor instead.
func (*ImageReference) Descriptor() ([]byte, []int) {
	return file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_rawDescGZIP(), []int{2}
}

func (x *ImageReference) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *ImageReference) GetIsPrefix() bool {
	if x != nil {
		return x.IsPrefix
	}
	return false
}

func (x *ImageReference) GetAllowOverwrite() bool {
	if x != nil {
		return x.AllowOverwrite
	}
	return false
}

func (x *ImageReference) GetAddDigest() bool {
	if x != nil {
		return x.AddDigest
	}
	return false
}

func (x *ImageReference) GetSkipNamedDigest() bool {
	if x != nil {
		return x.SkipNamedDigest
	}
	return false
}

var File_github_com_containerd_containerd_api_types_transfer_imagestore_proto protoreflect.FileDescriptor

var file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_rawDesc = []byte{
	0x0a, 0x44, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x6e,
	0x74, 0x61, 0x69, 0x6e, 0x65, 0x72, 0x64, 0x2f, 0x63, 0x6f, 0x6e, 0x74, 0x61, 0x69, 0x6e, 0x65,
	0x72, 0x64, 0x2f, 0x61, 0x70, 0x69, 0x2f, 0x74, 0x79, 0x70, 0x65, 0x73, 0x2f, 0x74, 0x72, 0x61,
	0x6e, 0x73, 0x66, 0x65, 0x72, 0x2f, 0x69, 0x6d, 0x61, 0x67, 0x65, 0x73, 0x74, 0x6f, 0x72, 0x65,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x19, 0x63, 0x6f, 0x6e, 0x74, 0x61, 0x69, 0x6e, 0x65,
	0x72, 0x64, 0x2e, 0x74, 0x79, 0x70, 0x65, 0x73, 0x2e, 0x74, 0x72, 0x61, 0x6e, 0x73, 0x66, 0x65,
	0x72, 0x1a, 0x39, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f,
	0x6e, 0x74, 0x61, 0x69, 0x6e, 0x65, 0x72, 0x64, 0x2f, 0x63, 0x6f, 0x6e, 0x74, 0x61, 0x69, 0x6e,
	0x65, 0x72, 0x64, 0x2f, 0x61, 0x70, 0x69, 0x2f, 0x74, 0x79, 0x70, 0x65, 0x73, 0x2f, 0x70, 0x6c,
	0x61, 0x74, 0x66, 0x6f, 0x72, 0x6d, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0xef, 0x03, 0x0a,
	0x0a, 0x49, 0x6d, 0x61, 0x67, 0x65, 0x53, 0x74, 0x6f, 0x72, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x6e,
	0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12,
	0x49, 0x0a, 0x06, 0x6c, 0x61, 0x62, 0x65, 0x6c, 0x73, 0x18, 0x02, 0x20, 0x03, 0x28, 0x0b, 0x32,
	0x31, 0x2e, 0x63, 0x6f, 0x6e, 0x74, 0x61, 0x69, 0x6e, 0x65, 0x72, 0x64, 0x2e, 0x74, 0x79, 0x70,
	0x65, 0x73, 0x2e, 0x74, 0x72, 0x61, 0x6e, 0x73, 0x66, 0x65, 0x72, 0x2e, 0x49, 0x6d, 0x61, 0x67,
	0x65, 0x53, 0x74, 0x6f, 0x72, 0x65, 0x2e, 0x4c, 0x61, 0x62, 0x65, 0x6c, 0x73, 0x45, 0x6e, 0x74,
	0x72, 0x79, 0x52, 0x06, 0x6c, 0x61, 0x62, 0x65, 0x6c, 0x73, 0x12, 0x38, 0x0a, 0x09, 0x70, 0x6c,
	0x61, 0x74, 0x66, 0x6f, 0x72, 0x6d, 0x73, 0x18, 0x03, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x1a, 0x2e,
	0x63, 0x6f, 0x6e, 0x74, 0x61, 0x69, 0x6e, 0x65, 0x72, 0x64, 0x2e, 0x74, 0x79, 0x70, 0x65, 0x73,
	0x2e, 0x50, 0x6c, 0x61, 0x74, 0x66, 0x6f, 0x72, 0x6d, 0x52, 0x09, 0x70, 0x6c, 0x61, 0x74, 0x66,
	0x6f, 0x72, 0x6d, 0x73, 0x12, 0x21, 0x0a, 0x0c, 0x61, 0x6c, 0x6c, 0x5f, 0x6d, 0x65, 0x74, 0x61,
	0x64, 0x61, 0x74, 0x61, 0x18, 0x04, 0x20, 0x01, 0x28, 0x08, 0x52, 0x0b, 0x61, 0x6c, 0x6c, 0x4d,
	0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0x12, 0x25, 0x0a, 0x0e, 0x6d, 0x61, 0x6e, 0x69, 0x66,
	0x65, 0x73, 0x74, 0x5f, 0x6c, 0x69, 0x6d, 0x69, 0x74, 0x18, 0x05, 0x20, 0x01, 0x28, 0x0d, 0x52,
	0x0d, 0x6d, 0x61, 0x6e, 0x69, 0x66, 0x65, 0x73, 0x74, 0x4c, 0x69, 0x6d, 0x69, 0x74, 0x12, 0x54,
	0x0a, 0x10, 0x65, 0x78, 0x74, 0x72, 0x61, 0x5f, 0x72, 0x65, 0x66, 0x65, 0x72, 0x65, 0x6e, 0x63,
	0x65, 0x73, 0x18, 0x06, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x29, 0x2e, 0x63, 0x6f, 0x6e, 0x74, 0x61,
	0x69, 0x6e, 0x65, 0x72, 0x64, 0x2e, 0x74, 0x79, 0x70, 0x65, 0x73, 0x2e, 0x74, 0x72, 0x61, 0x6e,
	0x73, 0x66, 0x65, 0x72, 0x2e, 0x49, 0x6d, 0x61, 0x67, 0x65, 0x52, 0x65, 0x66, 0x65, 0x72, 0x65,
	0x6e, 0x63, 0x65, 0x52, 0x0f, 0x65, 0x78, 0x74, 0x72, 0x61, 0x52, 0x65, 0x66, 0x65, 0x72, 0x65,
	0x6e, 0x63, 0x65, 0x73, 0x12, 0x23, 0x0a, 0x0d, 0x65, 0x78, 0x70, 0x6f, 0x72, 0x74, 0x5f, 0x69,
	0x6d, 0x61, 0x67, 0x65, 0x73, 0x18, 0x07, 0x20, 0x03, 0x28, 0x09, 0x52, 0x0c, 0x65, 0x78, 0x70,
	0x6f, 0x72, 0x74, 0x49, 0x6d, 0x61, 0x67, 0x65, 0x73, 0x12, 0x48, 0x0a, 0x07, 0x75, 0x6e, 0x70,
	0x61, 0x63, 0x6b, 0x73, 0x18, 0x0a, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x2e, 0x2e, 0x63, 0x6f, 0x6e,
	0x74, 0x61, 0x69, 0x6e, 0x65, 0x72, 0x64, 0x2e, 0x74, 0x79, 0x70, 0x65, 0x73, 0x2e, 0x74, 0x72,
	0x61, 0x6e, 0x73, 0x66, 0x65, 0x72, 0x2e, 0x55, 0x6e, 0x70, 0x61, 0x63, 0x6b, 0x43, 0x6f, 0x6e,
	0x66, 0x69, 0x67, 0x75, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x52, 0x07, 0x75, 0x6e, 0x70, 0x61,
	0x63, 0x6b, 0x73, 0x1a, 0x39, 0x0a, 0x0b, 0x4c, 0x61, 0x62, 0x65, 0x6c, 0x73, 0x45, 0x6e, 0x74,
	0x72, 0x79, 0x12, 0x10, 0x0a, 0x03, 0x6b, 0x65, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x03, 0x6b, 0x65, 0x79, 0x12, 0x14, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x02, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x3a, 0x02, 0x38, 0x01, 0x22, 0x6f,
	0x0a, 0x13, 0x55, 0x6e, 0x70, 0x61, 0x63, 0x6b, 0x43, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x75, 0x72,
	0x61, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x36, 0x0a, 0x08, 0x70, 0x6c, 0x61, 0x74, 0x66, 0x6f, 0x72,
	0x6d, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x63, 0x6f, 0x6e, 0x74, 0x61, 0x69,
	0x6e, 0x65, 0x72, 0x64, 0x2e, 0x74, 0x79, 0x70, 0x65, 0x73, 0x2e, 0x50, 0x6c, 0x61, 0x74, 0x66,
	0x6f, 0x72, 0x6d, 0x52, 0x08, 0x70, 0x6c, 0x61, 0x74, 0x66, 0x6f, 0x72, 0x6d, 0x12, 0x20, 0x0a,
	0x0b, 0x73, 0x6e, 0x61, 0x70, 0x73, 0x68, 0x6f, 0x74, 0x74, 0x65, 0x72, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x0b, 0x73, 0x6e, 0x61, 0x70, 0x73, 0x68, 0x6f, 0x74, 0x74, 0x65, 0x72, 0x22,
	0xb5, 0x01, 0x0a, 0x0e, 0x49, 0x6d, 0x61, 0x67, 0x65, 0x52, 0x65, 0x66, 0x65, 0x72, 0x65, 0x6e,
	0x63, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x1b, 0x0a, 0x09, 0x69, 0x73, 0x5f, 0x70, 0x72, 0x65,
	0x66, 0x69, 0x78, 0x18, 0x02, 0x20, 0x01, 0x28, 0x08, 0x52, 0x08, 0x69, 0x73, 0x50, 0x72, 0x65,
	0x66, 0x69, 0x78, 0x12, 0x27, 0x0a, 0x0f, 0x61, 0x6c, 0x6c, 0x6f, 0x77, 0x5f, 0x6f, 0x76, 0x65,
	0x72, 0x77, 0x72, 0x69, 0x74, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x08, 0x52, 0x0e, 0x61, 0x6c,
	0x6c, 0x6f, 0x77, 0x4f, 0x76, 0x65, 0x72, 0x77, 0x72, 0x69, 0x74, 0x65, 0x12, 0x1d, 0x0a, 0x0a,
	0x61, 0x64, 0x64, 0x5f, 0x64, 0x69, 0x67, 0x65, 0x73, 0x74, 0x18, 0x04, 0x20, 0x01, 0x28, 0x08,
	0x52, 0x09, 0x61, 0x64, 0x64, 0x44, 0x69, 0x67, 0x65, 0x73, 0x74, 0x12, 0x2a, 0x0a, 0x11, 0x73,
	0x6b, 0x69, 0x70, 0x5f, 0x6e, 0x61, 0x6d, 0x65, 0x64, 0x5f, 0x64, 0x69, 0x67, 0x65, 0x73, 0x74,
	0x18, 0x05, 0x20, 0x01, 0x28, 0x08, 0x52, 0x0f, 0x73, 0x6b, 0x69, 0x70, 0x4e, 0x61, 0x6d, 0x65,
	0x64, 0x44, 0x69, 0x67, 0x65, 0x73, 0x74, 0x42, 0x35, 0x5a, 0x33, 0x67, 0x69, 0x74, 0x68, 0x75,
	0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x63, 0x6f, 0x6e, 0x74, 0x61, 0x69, 0x6e, 0x65, 0x72, 0x64,
	0x2f, 0x63, 0x6f, 0x6e, 0x74, 0x61, 0x69, 0x6e, 0x65, 0x72, 0x64, 0x2f, 0x61, 0x70, 0x69, 0x2f,
	0x74, 0x79, 0x70, 0x65, 0x73, 0x2f, 0x74, 0x72, 0x61, 0x6e, 0x73, 0x66, 0x65, 0x72, 0x62, 0x06,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_rawDescOnce sync.Once
	file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_rawDescData = file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_rawDesc
)

func file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_rawDescGZIP() []byte {
	file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_rawDescOnce.Do(func() {
		file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_rawDescData = protoimpl.X.CompressGZIP(file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_rawDescData)
	})
	return file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_rawDescData
}

var file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_msgTypes = make([]protoimpl.MessageInfo, 4)
var file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_goTypes = []interface{}{
	(*ImageStore)(nil),          // 0: containerd.types.transfer.ImageStore
	(*UnpackConfiguration)(nil), // 1: containerd.types.transfer.UnpackConfiguration
	(*ImageReference)(nil),      // 2: containerd.types.transfer.ImageReference
	nil,                         // 3: containerd.types.transfer.ImageStore.LabelsEntry
	(*types.Platform)(nil),      // 4: containerd.types.Platform
}
var file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_depIdxs = []int32{
	3, // 0: containerd.types.transfer.ImageStore.labels:type_name -> containerd.types.transfer.ImageStore.LabelsEntry
	4, // 1: containerd.types.transfer.ImageStore.platforms:type_name -> containerd.types.Platform
	2, // 2: containerd.types.transfer.ImageStore.extra_references:type_name -> containerd.types.transfer.ImageReference
	1, // 3: containerd.types.transfer.ImageStore.unpacks:type_name -> containerd.types.transfer.UnpackConfiguration
	4, // 4: containerd.types.transfer.UnpackConfiguration.platform:type_name -> containerd.types.Platform
	5, // [5:5] is the sub-list for method output_type
	5, // [5:5] is the sub-list for method input_type
	5, // [5:5] is the sub-list for extension type_name
	5, // [5:5] is the sub-list for extension extendee
	0, // [0:5] is the sub-list for field type_name
}

func init() { file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_init() }
func file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_init() {
	if File_github_com_containerd_containerd_api_types_transfer_imagestore_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ImageStore); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*UnpackConfiguration); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ImageReference); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   4,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_goTypes,
		DependencyIndexes: file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_depIdxs,
		MessageInfos:      file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_msgTypes,
	}.Build()
	File_github_com_containerd_containerd_api_types_transfer_imagestore_proto = out.File
	file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_rawDesc = nil
	file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_goTypes = nil
	file_github_com_containerd_containerd_api_types_transfer_imagestore_proto_depIdxs = nil
}
