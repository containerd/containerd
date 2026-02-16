//go:build windows

package winapi

import (
	"unsafe"

	"github.com/Microsoft/go-winio/pkg/guid"
	"golang.org/x/sys/windows"
)

type g = guid.GUID
type FsHandle uintptr
type StreamHandle uintptr

type CimFsFileMetadata struct {
	Attributes uint32
	FileSize   int64

	CreationTime   windows.Filetime
	LastWriteTime  windows.Filetime
	ChangeTime     windows.Filetime
	LastAccessTime windows.Filetime

	SecurityDescriptorBuffer unsafe.Pointer
	SecurityDescriptorSize   uint32

	ReparseDataBuffer unsafe.Pointer
	ReparseDataSize   uint32

	ExtendedAttributes unsafe.Pointer
	EACount            uint32
}

type CimFsImagePath struct {
	ImageDir  *uint16
	ImageName *uint16
}

//sys CimMountImage(imagePath string, fsName string, flags uint32, volumeID *g) (hr error) = cimfs.CimMountImage?
//sys CimDismountImage(volumeID *g) (hr error) = cimfs.CimDismountImage?

//sys CimCreateImage(imagePath string, oldFSName *uint16, newFSName *uint16, cimFSHandle *FsHandle) (hr error) = cimwriter.CimCreateImage?
//sys CimCreateImage2(imagePath string, flags uint32, oldFSName *uint16, newFSName *uint16, cimFSHandle *FsHandle) (hr error) = cimwriter.CimCreateImage2?
//sys CimCloseImage(cimFSHandle FsHandle) = cimwriter.CimCloseImage?
//sys CimCommitImage(cimFSHandle FsHandle) (hr error) = cimwriter.CimCommitImage?

//sys CimCreateFile(cimFSHandle FsHandle, path string, file *CimFsFileMetadata, cimStreamHandle *StreamHandle) (hr error) = cimwriter.CimCreateFile?
//sys CimCloseStream(cimStreamHandle StreamHandle) (hr error) = cimwriter.CimCloseStream?
//sys CimWriteStream(cimStreamHandle StreamHandle, buffer uintptr, bufferSize uint32) (hr error) = cimwriter.CimWriteStream?
//sys CimDeletePath(cimFSHandle FsHandle, path string) (hr error) = cimwriter.CimDeletePath?
//sys CimCreateHardLink(cimFSHandle FsHandle, newPath string, oldPath string) (hr error) = cimwriter.CimCreateHardLink?
//sys CimCreateAlternateStream(cimFSHandle FsHandle, path string, size uint64, cimStreamHandle *StreamHandle) (hr error) = cimwriter.CimCreateAlternateStream?
//sys CimAddFsToMergedImage(cimFSHandle FsHandle, path string) (hr error) = cimwriter.CimAddFsToMergedImage?
//sys CimAddFsToMergedImage2(cimFSHandle FsHandle, path string, flags uint32) (hr error) = cimwriter.CimAddFsToMergedImage2?
//sys CimMergeMountImage(numCimPaths uint32, backingImagePaths *CimFsImagePath, flags uint32, volumeID *g) (hr error) = cimfs.CimMergeMountImage?
//sys CimTombstoneFile(cimFSHandle FsHandle, path string) (hr error) = cimwriter.CimTombstoneFile?
//sys CimCreateMergeLink(cimFSHandle FsHandle, newPath string, oldPath string) (hr error) = cimwriter.CimCreateMergeLink?
//sys CimSealImage(blockCimPath string, hashSize *uint64, fixedHeaderSize *uint64, hash *byte) (hr error) = cimwriter.CimSealImage?
//sys CimGetVerificationInformation(blockCimPath string, isSealed *uint32, hashSize *uint64, signatureSize *uint64, fixedHeaderSize *uint64, hash *byte, signature *byte) (hr error) = cimfs.CimGetVerificationInformation?
//sys CimMountVerifiedImage(imagePath string, fsName string, flags uint32, volumeID *g, hashSize uint16, hash *byte) (hr error) = cimfs.CimMountVerifiedImage?
//sys CimMergeMountVerifiedImage(numCimPaths uint32, backingImagePaths *CimFsImagePath, flags uint32, volumeID *g, hashSize uint16, hash *byte) (hr error) = cimfs.CimMergeMountVerifiedImage
